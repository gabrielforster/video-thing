terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# ASSUMPTION: HLS assets are laid out under the processed bucket as
#   <video_id>/master.m3u8                 (top-level multivariant playlist)
#   <video_id>/<rendition>/playlist.m3u8   (per-rendition media playlist)
#   <video_id>/<rendition>/<n>.ts          (media segments)
# CloudFront path_pattern matching is glob-only (no regex), so the patterns below key off the
# filename rather than a rendition-name capture group.

data "aws_cloudfront_cache_policy" "caching_optimized" {
  name = "Managed-CachingOptimized"
}

# Master manifest: very short TTL so a re-encode/renamed rendition is picked up almost immediately.
resource "aws_cloudfront_cache_policy" "master_manifest" {
  name        = "${var.project_name}-${var.environment}-hls-master-manifest"
  default_ttl = 5
  max_ttl     = 5
  min_ttl     = 0

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
  }
}

# Variant playlists change more slowly than the master but still need to reflect new segments
# during a live-ish window, hence a slightly longer TTL.
resource "aws_cloudfront_cache_policy" "variant_playlist" {
  name        = "${var.project_name}-${var.environment}-hls-variant-playlist"
  default_ttl = 30
  max_ttl     = 30
  min_ttl     = 0

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
  }
}

# Segments are immutable once written, so they can be cached hard for a full day at the edge.
resource "aws_cloudfront_cache_policy" "segments" {
  name        = "${var.project_name}-${var.environment}-hls-segments"
  default_ttl = 86400
  max_ttl     = 86400
  min_ttl     = 0

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
  }
}

resource "aws_cloudfront_origin_access_control" "this" {
  name                              = "${var.project_name}-${var.environment}-processed-oac"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
  origin_access_control_origin_type = "s3"
}

resource "aws_cloudfront_distribution" "this" {
  enabled     = true
  comment     = "${var.project_name}-${var.environment} processed asset CDN"
  price_class = var.price_class
  # No default_root_object: clients always request a specific manifest/segment path chosen by
  # the API, there's no "/" index to serve.

  origin {
    domain_name              = var.processed_bucket_regional_domain_name
    origin_id                = "processed-s3-origin"
    origin_access_control_id = aws_cloudfront_origin_access_control.this.id
  }

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "processed-s3-origin"
    viewer_protocol_policy = "redirect-to-https"
    cache_policy_id        = data.aws_cloudfront_cache_policy.caching_optimized.id
  }

  ordered_cache_behavior {
    path_pattern           = "*/master.m3u8"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "processed-s3-origin"
    viewer_protocol_policy = "redirect-to-https"
    cache_policy_id        = aws_cloudfront_cache_policy.master_manifest.id
  }

  ordered_cache_behavior {
    path_pattern           = "*/playlist.m3u8"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "processed-s3-origin"
    viewer_protocol_policy = "redirect-to-https"
    cache_policy_id        = aws_cloudfront_cache_policy.variant_playlist.id
  }

  ordered_cache_behavior {
    path_pattern           = "*.ts"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "processed-s3-origin"
    viewer_protocol_policy = "redirect-to-https"
    cache_policy_id        = aws_cloudfront_cache_policy.segments.id
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  # MVP uses the shared *.cloudfront.net certificate; swap for an ACM cert + Route53 alias
  # (viewer_certificate.acm_certificate_arn + aliases) once a custom domain is needed.
  viewer_certificate {
    cloudfront_default_certificate = true
  }

  tags = {
    Name        = "${var.project_name}-${var.environment}-processed-cdn"
    Project     = var.project_name
    Environment = var.environment
  }
}

# Grants CloudFront (via this distribution's OAC) read access to the processed bucket. Defined
# here rather than in the s3 module to avoid a circular module dependency (s3 -> cloudfront ->
# distribution ARN -> s3 bucket policy).
resource "aws_s3_bucket_policy" "processed" {
  bucket = var.processed_bucket_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowCloudFrontServicePrincipalReadOnly"
        Effect    = "Allow"
        Principal = { Service = "cloudfront.amazonaws.com" }
        Action    = "s3:GetObject"
        Resource  = "${var.processed_bucket_arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.this.arn
          }
        }
      }
    ]
  })
}
