#!/bin/sh
# Mirrors infrastructure/terraform/environments/dev bucket/queue naming.
set -e

awslocal s3api create-bucket --bucket video-thing-dev-raw-uploads
awslocal s3api create-bucket --bucket video-thing-dev-processed-assets

QUEUE_URL=$(awslocal sqs create-queue --queue-name video-thing-dev-video-processing --query QueueUrl --output text)
QUEUE_ARN=$(awslocal sqs get-queue-attributes --queue-url "$QUEUE_URL" --attribute-names QueueArn --query Attributes.QueueArn --output text)

awslocal s3api put-bucket-notification-configuration \
  --bucket video-thing-dev-raw-uploads \
  --notification-configuration '{"QueueConfigurations":[{"QueueArn":"'"$QUEUE_ARN"'","Events":["s3:ObjectCreated:*"]}]}'

# The browser uploads directly to the raw bucket and hls.js fetches
# playlists/segments from the processed bucket, both cross-origin from the
# Vite dev server. In AWS this is the CloudFront/S3 CORS configuration.
CORS='{"CORSRules":[{"AllowedOrigins":["*"],"AllowedMethods":["GET","PUT","HEAD"],"AllowedHeaders":["*"],"ExposeHeaders":["ETag"],"MaxAgeSeconds":3000}]}'

awslocal s3api put-bucket-cors --bucket video-thing-dev-raw-uploads --cors-configuration "$CORS"
awslocal s3api put-bucket-cors --bucket video-thing-dev-processed-assets --cors-configuration "$CORS"
