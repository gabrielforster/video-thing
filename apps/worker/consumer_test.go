package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const testVideoID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

type fakeSQS struct {
	deleted []string
}

func (f *fakeSQS) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return &sqs.ReceiveMessageOutput{}, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

type fakeWorkerStore struct {
	failed        []uuid.UUID
	markFailedErr error
	processingErr error
}

func (f *fakeWorkerStore) MarkProcessing(context.Context, uuid.UUID) (db.Video, error) {
	return db.Video{}, f.processingErr
}

func (f *fakeWorkerStore) MarkReady(context.Context, db.MarkReadyParams) (db.Video, error) {
	return db.Video{}, nil
}

func (f *fakeWorkerStore) MarkFailed(_ context.Context, arg db.MarkFailedParams) (db.Video, error) {
	f.failed = append(f.failed, arg.ID)
	if f.markFailedErr != nil {
		return db.Video{}, f.markFailedErr
	}
	return db.Video{ID: arg.ID, Status: db.VideoStatusFailed}, nil
}

type fakeProcessor struct {
	err   error
	calls int
}

func (f *fakeProcessor) process(context.Context, uploadedObject) error {
	f.calls++
	return f.err
}

func message(receiveCount string) types.Message {
	body := `{"Records":[{"s3":{"bucket":{"name":"video-thing-dev-raw-uploads"},` +
		`"object":{"key":"raw/` + testVideoID + `","size":42}}}]}`
	msg := types.Message{Body: aws.String(body), ReceiptHandle: aws.String("receipt-1")}
	if receiveCount != "" {
		msg.Attributes = map[string]string{
			string(types.MessageSystemAttributeNameApproximateReceiveCount): receiveCount,
		}
	}
	return msg
}

func newTestConsumer(p processor, s workerStore) (*consumer, *fakeSQS) {
	q := &fakeSQS{}
	return &consumer{sqs: q, queueURL: "http://queue.test/q", pipeline: p, store: s}, q
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})
	return &buf
}

func TestSuccessfulJobDeletesTheMessage(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{}
	c, q := newTestConsumer(&fakeProcessor{}, store)

	c.handle(context.Background(), message("1"))

	if len(q.deleted) != 1 {
		t.Fatalf("deleted %v, want the message deleted once", q.deleted)
	}
	if len(store.failed) != 0 {
		t.Fatalf("MarkFailed called for a successful job: %v", store.failed)
	}
}

func TestTransientFailureBelowTheCeilingLeavesTheMessageForRedelivery(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{}
	c, q := newTestConsumer(&fakeProcessor{err: errors.New("s3 timeout")}, store)

	c.handle(context.Background(), message("1"))

	if len(q.deleted) != 0 {
		t.Fatalf("deleted %v, want the message left for redelivery", q.deleted)
	}
	if len(store.failed) != 0 {
		t.Fatalf("MarkFailed called on attempt 1: %v", store.failed)
	}
}

func TestTransientFailureAtTheCeilingFailsTheRecordAndDeletes(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{}
	c, q := newTestConsumer(&fakeProcessor{err: errors.New("s3 timeout")}, store)

	c.handle(context.Background(), message("3"))

	if len(store.failed) != 1 || store.failed[0] != uuid.MustParse(testVideoID) {
		t.Fatalf("failed = %v, want MarkFailed for %s", store.failed, testVideoID)
	}
	if len(q.deleted) != 1 {
		t.Fatalf("deleted %v, want the message deleted once the ceiling is reached", q.deleted)
	}
}

func TestPermanentFailureFailsTheRecordOnTheFirstAttempt(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{}
	c, q := newTestConsumer(&fakeProcessor{err: permanent("ffprobe: invalid data")}, store)

	c.handle(context.Background(), message("1"))

	if len(store.failed) != 1 {
		t.Fatalf("failed = %v, want MarkFailed on attempt 1", store.failed)
	}
	if len(q.deleted) != 1 {
		t.Fatalf("deleted %v, want the message deleted on a permanent failure", q.deleted)
	}
}

func TestUnparseableMessageIsDeletedWithoutTouchingTheDatabase(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{}
	p := &fakeProcessor{}
	c, q := newTestConsumer(p, store)

	c.handle(context.Background(), types.Message{
		Body:          aws.String(`{"Service":"Amazon S3","Event":"s3:TestEvent"}`),
		ReceiptHandle: aws.String("receipt-1"),
	})

	if p.calls != 0 {
		t.Fatalf("pipeline ran %d times for a non-upload message", p.calls)
	}
	if len(store.failed) != 0 {
		t.Fatalf("MarkFailed called for a non-upload message: %v", store.failed)
	}
	if len(q.deleted) != 1 {
		t.Fatalf("deleted %v, want the poison message deleted", q.deleted)
	}
}

func TestFailureWriteForAMissingRowStillDeletesTheMessage(t *testing.T) {
	logs := captureLog(t)
	store := &fakeWorkerStore{markFailedErr: pgx.ErrNoRows}
	c, q := newTestConsumer(&fakeProcessor{err: permanent("no video row")}, store)

	c.handle(context.Background(), message("1"))

	if len(q.deleted) != 1 {
		t.Fatalf("deleted %v, want the message deleted when there is no row to fail", q.deleted)
	}
	if !strings.Contains(logs.String(), testVideoID) {
		t.Errorf("the discarded orphan should be logged with its id, got:\n%s", logs.String())
	}
}

func TestFailureWriteErrorOtherThanMissingRowLeavesTheMessage(t *testing.T) {
	captureLog(t)
	store := &fakeWorkerStore{markFailedErr: errors.New("connection refused")}
	c, q := newTestConsumer(&fakeProcessor{err: permanent("ffprobe: invalid data")}, store)

	c.handle(context.Background(), message("1"))

	if len(q.deleted) != 0 {
		t.Fatalf("deleted %v, want the message left so the failure can be recorded later", q.deleted)
	}
}

func TestMissingReceiveCountIsLoggedLoudly(t *testing.T) {
	logs := captureLog(t)

	if got := receiveCount(message("")); got != 1 {
		t.Fatalf("receiveCount = %d, want the conservative 1", got)
	}
	if !strings.Contains(logs.String(), "ApproximateReceiveCount") {
		t.Errorf("a missing ApproximateReceiveCount must be logged, got:\n%s", logs.String())
	}
}

func TestUnparseableReceiveCountIsLoggedLoudly(t *testing.T) {
	logs := captureLog(t)

	if got := receiveCount(message("not-a-number")); got != 1 {
		t.Fatalf("receiveCount = %d, want the conservative 1", got)
	}
	if !strings.Contains(logs.String(), "ApproximateReceiveCount") {
		t.Errorf("an unparseable ApproximateReceiveCount must be logged, got:\n%s", logs.String())
	}
}

func TestReceiveCountReadsTheAttribute(t *testing.T) {
	captureLog(t)
	if got := receiveCount(message("7")); got != 7 {
		t.Fatalf("receiveCount = %d, want 7", got)
	}
}
