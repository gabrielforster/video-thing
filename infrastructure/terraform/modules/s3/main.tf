terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

resource "aws_s3_bucket" "raw" {
  bucket = var.raw_bucket_name

  tags = {
    Name        = var.raw_bucket_name
    Project     = var.project_name
    Environment = var.environment
  }
}

resource "aws_s3_bucket_versioning" "raw" {
  bucket = aws_s3_bucket.raw.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "raw" {
  bucket                  = aws_s3_bucket.raw.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Needed so browsers can PUT/POST directly to S3 using presigned URLs without a server proxy.
resource "aws_s3_bucket_cors_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  cors_rule {
    allowed_methods = ["PUT", "POST"]
    allowed_origins = var.cors_allowed_origins
    allowed_headers = ["*"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }
}

resource "aws_s3_bucket_notification" "raw" {
  bucket = aws_s3_bucket.raw.id

  queue {
    queue_arn = var.sqs_queue_arn
    events    = ["s3:ObjectCreated:*"]
  }
}

# Abandoned/failed multipart uploads (common with large video files) would otherwise accrue
# storage cost forever; expire them after a week.
resource "aws_s3_bucket_lifecycle_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  rule {
    id     = "expire-incomplete-multipart-uploads"
    status = "Enabled"

    # Empty prefix filter = applies to every object in the bucket.
    filter {
      prefix = ""
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

resource "aws_s3_bucket" "processed" {
  bucket = var.processed_bucket_name

  tags = {
    Name        = var.processed_bucket_name
    Project     = var.project_name
    Environment = var.environment
  }
}

resource "aws_s3_bucket_versioning" "processed" {
  bucket = aws_s3_bucket.processed.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "processed" {
  bucket                  = aws_s3_bucket.processed.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# NOTE: the bucket policy granting cloudfront.amazonaws.com read access via the distribution's
# Origin Access Control is intentionally NOT defined here. Creating it here would require this
# module to know the CloudFront distribution ARN, and the cloudfront module needs this bucket's
# outputs to build its origin -- a circular dependency. Instead the cloudfront module owns an
# aws_s3_bucket_policy resource attached to this bucket (passed in via processed_bucket_id).
