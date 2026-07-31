# Task 4: DLQ, redrive policy, and the alarms that make it visible

> Task 4 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 3](task-03-ecs-task-definitions-pointing-at-real.md) · Next: [Task 5](task-05-one-cloudfront-distribution-in-front-web.md)

---

**Files:**
- Modify: `apps/worker/consumer.go` (extract `receiveInput`, raise `visibilityTimeoutSeconds`)
- Test: `apps/worker/consumer_test.go` (append one test)
- Modify: `infrastructure/terraform/modules/sqs/main.tf` (redrive-allow policy)
- Modify: `infrastructure/terraform/modules/sqs/outputs.tf` (append two outputs)
- Modify: `infrastructure/terraform/modules/monitoring/variables.tf` (append two variables)
- Modify: `infrastructure/terraform/modules/monitoring/main.tf` (region variable, DLQ series, DLQ alarm, outputs)
- Modify: `infrastructure/terraform/modules/monitoring/outputs.tf` (add the alarm ARN)
- Modify: `infrastructure/terraform/environments/dev/main.tf` (`sqs` and `monitoring` blocks)

**The number, and the reason.** `maxReceiveCount = 5`.

`apps/worker/consumer.go:18` caps the worker at `maxAttempts = 3`: on the third delivery of a message it writes `status = failed` with the error text and *deletes the message itself*. So under normal operation nothing ever reaches receive 4 — every failure the worker can see is already recorded in the database, and a DLQ set to 3 would only ever contain messages the application had already handled, making it noise rather than signal.

`maxReceiveCount` therefore has to sit strictly above 3, and it is a backstop for exactly one class of event: the worker could not record the failure or could not delete the message. That happens when `MarkFailed` itself errors (`consumer.go:100` logs and returns *without* deleting, on purpose, so the message is redelivered once Postgres recovers), or when the task dies before the delete — OOM kill, Fargate scale-in during a job, a SIGKILL. Two extra deliveries past the worker's own ceiling give a transient database outage roughly two visibility windows to recover on its own; the sixth receive redrives to the DLQ, where a 14-day retention keeps the message for a human to inspect and `aws sqs start-message-move-task` back onto the main queue.

`maxReceiveCount = 4` would leave no recovery room past the ceiling. Anything above 5 delays the DLQ signal for a message that is, by then, definitely stuck.

**The interaction this exposes, and the one-line fix.** `maxReceiveCount` only means what the paragraph above claims if `ApproximateReceiveCount` counts *failures*. It does not today: `consumer.go:20` sets `visibilityTimeoutSeconds = 120` and passes it as the per-receive `VisibilityTimeout`, which overrides the queue's 900. A transcode that takes longer than two minutes — which, with the full ladder from worker-rendition-ladder-plan.md, is most of them — has its message become visible again while the first attempt is still running, so `ApproximateReceiveCount` climbs with wall-clock time rather than with failures. A six-minute job would burn three receives on its first attempt and could land in the DLQ while it is succeeding. Raising the constant to 900 to match `modules/sqs/variables.tf`'s `visibility_timeout_seconds` makes the count mean what the redrive policy assumes. It is one constant, not a feature, so it belongs here rather than in the ladder plan — **note for coordination:** [worker-rendition-ladder-plan.md](worker-rendition-ladder-plan.md) also edits `apps/worker/consumer.go`, so whichever plan lands second resolves a one-line conflict.

**What 900 does not fix.** There is still no `ChangeMessageVisibility` heartbeat, so a
job that outruns 900 seconds is redelivered while the first attempt is still running:
two workers transcode the same source concurrently, and since `MarkReady`/`MarkFailed`
are unguarded on `status`, the final row state depends on write order. 900 seconds
buys enough headroom that the four-rendition ladder does not hit this on realistic
sources, and it makes the DLQ signal meaningful, which is what this task is for — it
is not a substitute for the heartbeat. Raising the input size limit, or adding a
rendition above 1080p, needs the heartbeat first. Do not add it here; it is worker
behaviour, not delivery, and it needs its own tests around the concurrent-write race.

- [ ] **Step 1: Write the failing test**

Append to `apps/worker/consumer_test.go`:

```go
func TestReceiveInputVisibilityTimeoutMatchesTheQueue(t *testing.T) {
	in := receiveInput("https://sqs.us-east-1.amazonaws.com/000000000000/q")

	if in.VisibilityTimeout != 900 {
		t.Fatalf("VisibilityTimeout = %d, want 900", in.VisibilityTimeout)
	}
	if in.WaitTimeSeconds != 20 {
		t.Fatalf("WaitTimeSeconds = %d, want 20", in.WaitTimeSeconds)
	}
	if in.MaxNumberOfMessages != 1 {
		t.Fatalf("MaxNumberOfMessages = %d, want 1", in.MaxNumberOfMessages)
	}
	if aws.ToString(in.QueueUrl) != "https://sqs.us-east-1.amazonaws.com/000000000000/q" {
		t.Fatalf("QueueUrl = %q", aws.ToString(in.QueueUrl))
	}
	if len(in.MessageSystemAttributeNames) != 1 ||
		in.MessageSystemAttributeNames[0] != types.MessageSystemAttributeNameApproximateReceiveCount {
		t.Fatalf("MessageSystemAttributeNames = %v, want [ApproximateReceiveCount]", in.MessageSystemAttributeNames)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./apps/worker -run TestReceiveInputVisibilityTimeoutMatchesTheQueue -v`

Expected: FAIL to build with `./consumer_test.go:NN:8: undefined: receiveInput`.

- [ ] **Step 3: Extract `receiveInput` and raise the timeout in `apps/worker/consumer.go`**

Replace line 20:

```go
const visibilityTimeoutSeconds = 120
```

with:

```go
// Must match the queue's own visibility_timeout_seconds (infrastructure/terraform/modules/sqs).
// A shorter per-receive value makes SQS redeliver a message while the first attempt is still
// transcoding, which inflates ApproximateReceiveCount with wall-clock time instead of with
// failures and can redrive a succeeding job to the dead-letter queue.
const visibilityTimeoutSeconds = 900
```

Replace the inline `ReceiveMessageInput` literal in `run` (lines 46-54):

```go
		out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   visibilityTimeoutSeconds,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameApproximateReceiveCount,
			},
		})
```

with:

```go
		out, err := c.sqs.ReceiveMessage(ctx, receiveInput(c.queueURL))
```

and add the constructor immediately after the `consumer` struct definition (after line 38):

```go
func receiveInput(queueURL string) *sqs.ReceiveMessageInput {
	return &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     20,
		VisibilityTimeout:   visibilityTimeoutSeconds,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameApproximateReceiveCount,
		},
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./apps/worker/... && gofmt -l . && go vet ./...`
Expected: PASS, `gofmt -l .` prints nothing, vet clean.

- [ ] **Step 5: Add the DLQ redrive-allow policy and name outputs**

Append to `infrastructure/terraform/modules/sqs/main.tf`:

```hcl
# Without this the DLQ refuses `aws sqs start-message-move-task`, so a message that lands in
# it can be read but never replayed onto the main queue -- which is the only reason to keep it.
resource "aws_sqs_queue_redrive_allow_policy" "dlq" {
  queue_url = aws_sqs_queue.dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.this.arn]
  })
}
```

Append to `infrastructure/terraform/modules/sqs/outputs.tf`:

```hcl
output "queue_name" {
  description = "Name of the main processing queue, for CloudWatch metric dimensions."
  value       = aws_sqs_queue.this.name
}

output "dlq_name" {
  description = "Name of the dead-letter queue, for CloudWatch metric dimensions."
  value       = aws_sqs_queue.dlq.name
}
```

- [ ] **Step 6: Add the DLQ alarm to the `monitoring` module**

Append to `infrastructure/terraform/modules/monitoring/variables.tf`:

```hcl
variable "dlq_queue_name" {
  description = "Name of the dead-letter queue (not the ARN/URL) for CloudWatch metric dimensions."
  type        = string
}

variable "aws_region" {
  description = "AWS region the dashboard widgets query."
  type        = string
  default     = "us-east-1"
}
```

In `infrastructure/terraform/modules/monitoring/main.tf`, replace all five occurrences of `region  = "us-east-1"` and `region = "us-east-1"` inside `dashboard_body` with `region = var.aws_region`. Then replace the SQS widget (the fifth widget, `title = "SQS Queue Depth (Visible Messages)"`) with:

```hcl
      {
        type   = "metric"
        x      = 0
        y      = 12
        width  = 12
        height = 6
        properties = {
          title  = "SQS Queue Depth (Visible Messages)"
          view   = "timeSeries"
          region = var.aws_region
          metrics = [
            ["AWS/SQS", "ApproximateNumberOfMessagesVisible", "QueueName", var.sqs_queue_name, { stat = "Average", label = "main" }],
            ["AWS/SQS", "ApproximateNumberOfMessagesVisible", "QueueName", var.dlq_queue_name, { stat = "Maximum", label = "dlq" }]
          ]
        }
      }
```

Append the alarm to the same file:

```hcl
# Any message in the DLQ is a message the worker could neither process nor record a failure
# for, so the threshold is zero and one period is enough -- there is no such thing as an
# acceptable steady-state DLQ depth. treat_missing_data is notBreaching because SQS stops
# publishing metrics for a queue that has been empty and untouched for six hours.
resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  alarm_name          = "${var.project_name}-${var.environment}-dlq-not-empty"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 300
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  statistic           = "Maximum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "A video-processing message reached the dead-letter queue: the worker failed and could not record the failure. Inspect it, then redrive with `aws sqs start-message-move-task`."
  dimensions = {
    QueueName = var.dlq_queue_name
  }
  alarm_actions = local.alarm_actions
  ok_actions    = local.alarm_actions

  tags = {
    Name        = "${var.project_name}-${var.environment}-dlq-not-empty"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

In `infrastructure/terraform/modules/monitoring/outputs.tf`, add the new alarm to the merge:

```hcl
output "alarm_arns" {
  description = "Map of alarm name to ARN, for wiring into external alerting/documentation."
  value = merge(
    { sqs_queue_depth = aws_cloudwatch_metric_alarm.sqs_queue_depth.arn },
    { dlq_not_empty = aws_cloudwatch_metric_alarm.dlq_not_empty.arn },
    { alb_5xx = aws_cloudwatch_metric_alarm.alb_5xx.arn },
    { for k, a in aws_cloudwatch_metric_alarm.ecs_cpu_high : "ecs_cpu_high_${k}" => a.arn }
  )
}
```

The queue-depth alarm (`ApproximateNumberOfMessagesVisible > 100` over three 5-minute periods) already exists in the module and needs no change; it stays as the "workers are not keeping up" signal, distinct from the DLQ's "this message is unprocessable".

- [ ] **Step 7: Wire the environment**

In `environments/dev/main.tf`, replace the `module "sqs"` block with:

```hcl
module "sqs" {
  source = "../../modules/sqs"

  project_name   = local.project_name
  environment    = local.environment
  queue_name     = "${local.project_name}-${local.environment}-video-processing"
  raw_bucket_arn = "arn:aws:s3:::${local.project_name}-${local.environment}-raw-uploads"

  # Must cover a full transcode, and must match apps/worker/consumer.go's
  # visibilityTimeoutSeconds -- a shorter per-receive override there would redeliver a
  # message mid-flight and inflate ApproximateReceiveCount with wall-clock time.
  visibility_timeout_seconds = 900

  # Strictly above the worker's own maxAttempts of 3. The worker records status=failed and
  # deletes the message on its third failure, so receives 4 and 5 only happen when recording
  # the failure itself failed, or the task died before deleting. See docs/plans/delivery-plan.md
  # Task 4 for the full reasoning.
  max_receive_count = 5

  # NOTE: raw_bucket_arn is constructed rather than passed from module.s3 to
  # break the s3 <-> sqs cycle (s3's bucket notification needs the queue ARN,
  # the queue policy needs the bucket ARN). Bucket naming is deterministic
  # (see raw_bucket_name above), so this stays in sync without a real cycle.
}
```

In `module "monitoring"`, replace the `sqs_queue_name` line and add two arguments:

```hcl
  sqs_queue_name              = module.sqs.queue_name
  dlq_queue_name              = module.sqs.dlq_name
  aws_region                  = var.aws_region
```

- [ ] **Step 8: Validate**

```bash
cd infrastructure/terraform
terraform fmt -check -recursive
cd environments/dev
terraform init -backend=false
terraform validate
cd ../../../..
go test ./...
```

Expected: `Success! The configuration is valid.` and green Go tests.

- [ ] **Step 9: [AWS ONLY] Verify the queue attributes and force one message into the DLQ**

After Task 8's first apply:

```bash
QUEUE_URL="$(aws sqs get-queue-url --queue-name video-thing-dev-video-processing --query QueueUrl --output text)"
aws sqs get-queue-attributes --queue-url "$QUEUE_URL" \
  --attribute-names RedrivePolicy VisibilityTimeout --output json
```

Expected: `VisibilityTimeout` is `900`, and `RedrivePolicy` decodes to `maxReceiveCount: 5` with a `deadLetterTargetArn` ending in `-video-processing-dlq`.

Then prove the backstop actually works by sending a message the worker cannot parse into a video ID *and* cannot delete — the simplest reproduction is to scale the worker to zero and receive the message six times by hand:

```bash
aws sqs send-message --queue-url "$QUEUE_URL" --message-body '{"Records":[{"s3":{"bucket":{"name":"x"},"object":{"key":"raw/not-a-uuid","size":1}}}]}'
for i in $(seq 1 6); do
  aws sqs receive-message --queue-url "$QUEUE_URL" --visibility-timeout 1 >/dev/null
  sleep 2
done
DLQ_URL="$(aws sqs get-queue-url --queue-name video-thing-dev-video-processing-dlq --query QueueUrl --output text)"
aws sqs get-queue-attributes --queue-url "$DLQ_URL" --attribute-names ApproximateNumberOfMessages
```

Expected: `ApproximateNumberOfMessages` is `1`, and within ten minutes the `video-thing-dev-dlq-not-empty` alarm shows `ALARM`:

```bash
aws cloudwatch describe-alarms --alarm-names video-thing-dev-dlq-not-empty \
  --query 'MetricAlarms[0].[StateValue,StateReason]' --output text
```

Purge the DLQ afterwards: `aws sqs purge-queue --queue-url "$DLQ_URL"`. Scale the worker back with `aws ecs update-service --cluster video-thing-dev-cluster --service video-thing-dev-worker --desired-count 0` if you changed it.

- [ ] **Step 10: Commit**

```bash
git add apps/worker/consumer.go apps/worker/consumer_test.go \
  infrastructure/terraform/modules/sqs infrastructure/terraform/modules/monitoring \
  infrastructure/terraform/environments/dev/main.tf
git commit -m "feat: wire the DLQ redrive policy and its alarm, and align the worker visibility timeout"
```

---
