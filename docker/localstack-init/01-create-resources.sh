#!/bin/sh
# Mirrors infrastructure/terraform/environments/dev bucket/queue naming.
set -e

awslocal s3api create-bucket --bucket video-platform-dev-raw-uploads
awslocal s3api create-bucket --bucket video-platform-dev-processed-assets

QUEUE_URL=$(awslocal sqs create-queue --queue-name video-platform-dev-video-processing --query QueueUrl --output text)
QUEUE_ARN=$(awslocal sqs get-queue-attributes --queue-url "$QUEUE_URL" --attribute-names QueueArn --query Attributes.QueueArn --output text)

awslocal s3api put-bucket-notification-configuration \
  --bucket video-platform-dev-raw-uploads \
  --notification-configuration '{"QueueConfigurations":[{"QueueArn":"'"$QUEUE_ARN"'","Events":["s3:ObjectCreated:*"]}]}'
