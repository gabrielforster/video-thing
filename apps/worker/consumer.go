package main

import (
	"context"
	"errors"
	"log"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const maxAttempts = 3

type consumer struct {
	sqs      *sqs.Client
	queueURL string
	pipeline *pipeline
	store    workerStore
}

func (c *consumer) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   900,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameApproximateReceiveCount,
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("receive: %v", err)
			continue
		}

		for _, msg := range out.Messages {
			c.handle(ctx, msg)
		}
	}
}

func (c *consumer) handle(ctx context.Context, msg types.Message) {
	job, err := parseUpload(aws.ToString(msg.Body))
	if errors.Is(err, errNotAnUpload) {
		log.Printf("discarding message: %v", err)
		c.delete(ctx, msg)
		return
	}

	log.Printf("processing video %s from %s/%s", job.VideoID, job.Bucket, job.Key)

	if err := c.pipeline.process(ctx, job); err != nil {
		var perm *permanentError
		attempt := receiveCount(msg)

		if errors.As(err, &perm) || attempt >= maxAttempts {
			log.Printf("video %s failed permanently on attempt %d: %v", job.VideoID, attempt, err)
			reason := err.Error()
			if _, dbErr := c.store.MarkFailed(ctx, db.MarkFailedParams{
				ID:           job.VideoID,
				ErrorMessage: &reason,
			}); dbErr != nil {
				log.Printf("video %s: could not record failure: %v", job.VideoID, dbErr)
				return
			}
			c.delete(ctx, msg)
			return
		}

		log.Printf("video %s failed on attempt %d, will retry: %v", job.VideoID, attempt, err)
		return
	}

	log.Printf("video %s ready", job.VideoID)
	c.delete(ctx, msg)
}

func receiveCount(msg types.Message) int {
	raw := msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 1
	}
	return n
}

func (c *consumer) delete(ctx context.Context, msg types.Message) {
	if _, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		log.Printf("delete message: %v", err)
	}
}
