# Task 5: One CloudFront distribution in front of the web app, the API, and playback

> Task 5 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 4](task-04-dlq-redrive-policy-alarms-make-it.md) · Next: [Task 6](task-06-one-time-aws-bootstrap-state-bucket.md)

---

**Files:**
- Modify: `infrastructure/terraform/modules/s3/variables.tf` (add `web_bucket_name`, remove a dead variable)
- Modify: `infrastructure/terraform/modules/s3/main.tf` (the private web bucket, processed-bucket CORS)
- Modify: `infrastructure/terraform/modules/s3/outputs.tf` (append three web-bucket outputs)
- Modify: `infrastructure/terraform/modules/cloudfront/variables.tf` (append four variables)
- Modify: `infrastructure/terraform/modules/cloudfront/main.tf` (three origins, nine behaviours, CORS policy, SPA fallback, web bucket policy)
- Modify: `infrastructure/terraform/modules/cloudfront/outputs.tf` (append `distribution_url`)
- Modify: `infrastructure/terraform/modules/alb/main.tf` (narrow ingress to CloudFront's managed prefix list)
- Modify: `infrastructure/terraform/environments/dev/main.tf` (`s3`, `cloudfront` blocks)
- Modify: `infrastructure/terraform/environments/dev/outputs.tf` (append three outputs)

**No application code changes.** Two contracts hold at once here, and both were verified against the source rather than assumed:

1. **Contract 4 — asset URLs.** The API stores S3 keys and prepends `PUBLIC_ASSET_BASE_URL` (`apps/api/handlers.go` `assetURL`). Playback moving to the CDN is a change to one environment variable's value. `PUBLIC_ASSET_BASE_URL` was already set to `https://${module.cloudfront.distribution_domain_name}` in Task 3, because the API cannot boot without a value; this task makes that URL serve bytes.
2. **The API base URL — no code change either.** `apps/web/src/api.ts:29` reads `const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'`. Building with `VITE_API_URL` set to the **empty string** makes `API_URL` the empty string — `??` is nullish coalescing, and `''` is not nullish, so the fallback does not fire — and every call becomes a root-relative path (`fetch('/videos')`). Because the app and the API arrive on the same CloudFront hostname, those relative paths resolve to the API. Verified by building it: with `VITE_API_URL=` the emitted bundle contains no occurrence of `localhost:8080` and the fetch targets are `/videos`. Step 10 re-asserts that in the pipeline so a Vite upgrade cannot silently reintroduce the fallback.

That same-origin arrangement is what removes the mixed-content problem: a page served over HTTPS from CloudFront calling an HTTP ALB hostname would be blocked by the browser, and there is no HTTPS on the ALB without a domain and an ACM certificate.

**The topology.** One distribution, default certificate, three origins:

| Behaviour | Origin | Cache policy | Why |
|---|---|---|---|
| `/videos` | ALB | `Managed-CachingDisabled` | `openapi.yaml` `/videos` — `GET` list and `POST` create |
| `/videos/*` | ALB | `Managed-CachingDisabled` | `openapi.yaml` `/videos/{id}` and `/videos/{id}/complete` |
| `/healthz` | ALB | `Managed-CachingDisabled` | `openapi.yaml` `/healthz` |
| `/readyz` | ALB | `Managed-CachingDisabled` | `openapi.yaml` `/readyz` |
| `/processed/*/master.m3u8` | processed bucket (OAC) | 5 s | §15 master playlist |
| `/processed/*/playlist.m3u8` | processed bucket (OAC) | 30 s | §15 variant playlists |
| `/processed/*.ts` | processed bucket (OAC) | 24 h | §15 segments, immutable filenames |
| `/processed/*` | processed bucket (OAC) | `Managed-CachingOptimized` | thumbnails (`thumbnails/cover.jpg`, `thumbnails/{second}.jpg`) match none of the three above |
| *default* | web bucket (OAC) | `Managed-CachingOptimized` | the Vite build |

Those five API path patterns come from `openapi.yaml`'s `paths` map and nothing else — `/videos`, `/videos/{id}`, `/videos/{id}/complete`, `/healthz`, `/readyz`, collapsed to `/videos`, `/videos/*`, `/healthz`, `/readyz`. Do not add a pattern for a route that does not exist in that file.

**Ordering matters twice.** CloudFront evaluates `ordered_cache_behavior` blocks top to bottom and takes the first match, and `*` matches across `/`. So the three §15 patterns must precede the `/processed/*` catch-all, or thumbnails and segments would collapse into one TTL. And the existing module's patterns (`*/master.m3u8`, `*/playlist.m3u8`, `*.ts`) must gain the `/processed/` prefix: with a web bundle now on the same distribution, a bare `*.ts` would route a TypeScript source map or any `.ts` asset to the wrong origin.

**What already exists and needs no work:** `modules/cloudfront/main.tf` already has the Origin Access Control, the `aws_s3_bucket_policy.processed` granting `cloudfront.amazonaws.com` `s3:GetObject` scoped by `AWS:SourceArn`, and the three §15 cache policies with the right TTLs. The processed bucket is already private — all four `aws_s3_bucket_public_access_block.processed` flags are `true` and there is no public-read policy to remove. Step 1 verifies that rather than trusting this paragraph.

**Three things this trades away. All deliberate, all recorded here rather than discovered later.**

1. **CloudFront reaches the ALB over plain HTTP.** `modules/alb/main.tf` has one listener, on port 80. The viewer-to-CloudFront hop is HTTPS (`viewer_protocol_policy = "redirect-to-https"`); the CloudFront-to-ALB hop is not, so API request and response bodies cross AWS's network unencrypted. Fixing it takes three things and a decision nobody has made: a domain name, an ACM certificate on the ALB, and `custom_origin_config.origin_protocol_policy = "https-only"` on the ALB origin. Deliberately deferred for a demo — and it is strictly better than the alternative this replaces, where the *browser* would have talked to that HTTP endpoint across the public internet.
2. **The ALB stays internet-facing, but stops answering the internet.** It cannot become `internal = true`: CloudFront can only reach a private ALB through VPC origins, a newer feature and another moving part. Instead its security group ingress narrows from `0.0.0.0/0` to the AWS-managed prefix list `com.amazonaws.global.cloudfront.origin-facing`, which is free and means the ALB's own hostname stops serving anyone. Residual risk: that prefix list covers *every* CloudFront distribution, including other AWS accounts', so this is not origin authentication. Closing that needs a shared secret header on the origin plus a check in the API — one more thing to rotate, not worth it for a demo. Note also that a prefix-list reference consumes rule capacity equal to the list's max entries (~55 of the 60-rule default), which is fine with one ingress and one egress rule but leaves no room to add many more.
3. **The SPA fallback maps 403 only, not 404.** More detail in step 6 — mapping 404 as well would corrupt the API contract.

- [ ] **Step 1: Confirm the existing OAC and privacy invariants**

```bash
grep -n 'origin_access_control_id\|aws_cloudfront_origin_access_control\|AllowCloudFrontServicePrincipal\|AWS:SourceArn' \
  infrastructure/terraform/modules/cloudfront/main.tf
grep -n -A6 'aws_s3_bucket_public_access_block" "processed"' infrastructure/terraform/modules/s3/main.tf
grep -rn 'acl\s*=\|PublicRead' infrastructure/terraform/modules/s3/main.tf
grep -n 'port\|protocol' infrastructure/terraform/modules/alb/main.tf | grep -i listener -A2
```

Expected: the OAC resource exists and is referenced from the `origin` block; the bucket policy statement is conditioned on the distribution ARN; all four `block_*`/`ignore_*`/`restrict_*` flags are `true`; the third grep prints nothing — no ACL, no public-read grant; and the ALB has exactly one listener, HTTP on 80, which is the fact trade-off 1 above rests on.

- [ ] **Step 2: Confirm the empty-`VITE_API_URL` claim before building anything on it**

```bash
cd apps/web
VITE_API_URL= pnpm build
grep -r 'localhost:8080' dist/assets/ && echo "FAIL: the bundle still points at localhost" && exit 1
grep -o '"/videos"' dist/assets/index-*.js | head -1
cd ../..
```

Expected: the build succeeds, the `localhost:8080` grep prints nothing (and so does not trigger the `FAIL` branch), and the last grep finds `"/videos"` — a root-relative path. If `localhost:8080` *is* present, Vite dropped the empty-string variable and the rest of this task's same-origin premise is false; stop and reconsider before writing any Terraform.

- [ ] **Step 3: Add the private web bucket to the `s3` module**

Append to `infrastructure/terraform/modules/s3/variables.tf`:

```hcl
variable "web_bucket_name" {
  description = "Globally-unique bucket name for the built web application, served as the CloudFront default origin."
  type        = string
}
```

Delete the `cloudfront_oac_id` variable from the same file (currently lines 32-36). It is passed by nobody and used by nothing — the bucket policies live in the `cloudfront` module, as the comment at the bottom of `modules/s3/main.tf` explains, and a dead input implies a wiring that does not exist.

Append to `infrastructure/terraform/modules/s3/main.tf`:

```hcl
# CloudFront's response-headers policy covers cached GETs, but a preflight OPTIONS is
# forwarded to the origin, and an S3 bucket with no CORS configuration answers it 403. Both
# layers are needed: this one for preflight, the edge policy for every actual response.
resource "aws_s3_bucket_cors_configuration" "processed" {
  bucket = aws_s3_bucket.processed.id

  cors_rule {
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = var.cors_allowed_origins
    allowed_headers = ["*"]
    expose_headers  = ["ETag", "Content-Length"]
    max_age_seconds = 3000
  }
}

# The built Vite bundle. Private like the processed bucket: reachable only through the
# distribution's OAC, never over an S3 URL. No website configuration -- S3 website endpoints
# cannot be used with OAC, and the SPA fallback is handled at the edge instead.
resource "aws_s3_bucket" "web" {
  bucket = var.web_bucket_name

  tags = {
    Name        = var.web_bucket_name
    Project     = var.project_name
    Environment = var.environment
  }
}

resource "aws_s3_bucket_public_access_block" "web" {
  bucket                  = aws_s3_bucket.web.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
```

The web bucket deliberately gets no `aws_s3_bucket_versioning`: every object in it is a build artifact reproducible from a commit SHA, and versioning would accumulate storage cost for nothing.

Append to `infrastructure/terraform/modules/s3/outputs.tf`:

```hcl
output "web_bucket_id" {
  description = "Name (ID) of the web application bucket, used as the `aws s3 sync` target in the deploy pipeline."
  value       = aws_s3_bucket.web.id
}

output "web_bucket_arn" {
  description = "ARN of the web application bucket."
  value       = aws_s3_bucket.web.arn
}

output "web_bucket_regional_domain_name" {
  description = "Regional domain name of the web application bucket, used as the CloudFront default origin."
  value       = aws_s3_bucket.web.bucket_regional_domain_name
}
```

- [ ] **Step 4: Add the `cloudfront` module's new variables**

Append to `infrastructure/terraform/modules/cloudfront/variables.tf`:

```hcl
variable "cors_allowed_origins" {
  description = "Origins allowed to fetch HLS playlists and segments. hls.js reads every playlist and segment with XHR, so without a matching Access-Control-Allow-Origin the player fails with manifestLoadError."
  type        = list(string)
  default     = ["*"]
}

variable "web_bucket_id" {
  description = "Name (ID) of the web application bucket, from the s3 module. This module owns its bucket policy for the same reason it owns the processed bucket's."
  type        = string
}

variable "web_bucket_arn" {
  description = "ARN of the web application bucket, from the s3 module."
  type        = string
}

variable "web_bucket_regional_domain_name" {
  description = "Regional domain name of the web application bucket, used as the default origin."
  type        = string
}

variable "alb_dns_name" {
  description = "DNS name of the API load balancer, used as the origin for the paths openapi.yaml defines."
  type        = string
}
```

- [ ] **Step 5: Add the CORS response-headers policy and the two managed-policy lookups**

The three §15 cache policies set `header_behavior = "none"`, so the viewer's `Origin` header never reaches S3 and S3 never returns `Access-Control-Allow-Origin`. hls.js fetches every playlist and segment with XHR, so without that header playback fails with `manifestLoadError` and an opaque console message — the classic "works from the S3 URL, breaks behind the CDN" failure. Forwarding `Origin` instead would fragment the cache per origin for no benefit, so the headers are added at the edge.

Insert into `infrastructure/terraform/modules/cloudfront/main.tf`, immediately before `resource "aws_cloudfront_origin_access_control" "this"`:

```hcl
data "aws_cloudfront_cache_policy" "caching_disabled" {
  name = "Managed-CachingDisabled"
}

# AllViewer forwards every header, cookie, and query string to the ALB. The API is dynamic
# and authenticates nothing, so there is nothing to cache and nothing to strip.
data "aws_cloudfront_origin_request_policy" "all_viewer" {
  name = "Managed-AllViewer"
}

resource "aws_cloudfront_response_headers_policy" "hls_cors" {
  name    = "${var.project_name}-${var.environment}-hls-cors"
  comment = "CORS headers for hls.js playlist and segment fetches"

  cors_config {
    access_control_allow_credentials = false
    origin_override                  = true

    access_control_allow_headers {
      items = ["*"]
    }

    access_control_allow_methods {
      items = ["GET", "HEAD", "OPTIONS"]
    }

    access_control_allow_origins {
      items = var.cors_allowed_origins
    }

    access_control_expose_headers {
      items = ["Content-Length", "Content-Range", "ETag"]
    }

    access_control_max_age_sec = 3000
  }
}
```

Also widen the OAC's name, since it now signs requests to two buckets rather than one — replace `name = "${var.project_name}-${var.environment}-processed-oac"` with:

```hcl
  name = "${var.project_name}-${var.environment}-s3-oac"
```

One OAC serves both S3 origins: an OAC is not bound to a bucket, it only says "sign S3 origin requests with SigV4", and the per-bucket policies are what actually scope access.

- [ ] **Step 6: Rewrite the distribution with three origins and nine behaviours**

Replace the whole `resource "aws_cloudfront_distribution" "this"` block (from `enabled = true` through the closing `tags` block) with:

```hcl
resource "aws_cloudfront_distribution" "this" {
  enabled             = true
  comment             = "${var.project_name}-${var.environment} web app, API, and processed assets"
  price_class         = var.price_class
  default_root_object = "index.html"

  origin {
    domain_name              = var.web_bucket_regional_domain_name
    origin_id                = "web-s3-origin"
    origin_access_control_id = aws_cloudfront_origin_access_control.this.id
  }

  origin {
    domain_name              = var.processed_bucket_regional_domain_name
    origin_id                = "processed-s3-origin"
    origin_access_control_id = aws_cloudfront_origin_access_control.this.id
  }

  # http-only, port 80: modules/alb has a single HTTP listener. This hop is unencrypted
  # inside AWS -- see the trade-offs at the top of Task 5. Fixing it needs a domain, an ACM
  # certificate on the ALB, and origin_protocol_policy = "https-only" here.
  origin {
    domain_name = var.alb_dns_name
    origin_id   = "api-alb-origin"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "http-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  # The API paths, first and uncached. These five patterns are exactly openapi.yaml's
  # `paths` map -- /videos, /videos/{id}, /videos/{id}/complete, /healthz, /readyz -- and
  # nothing else. All seven methods are allowed because POST and DELETE are part of the
  # contract; CloudFront has no narrower valid set that includes them.
  ordered_cache_behavior {
    path_pattern             = "/videos"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    target_origin_id         = "api-alb-origin"
    viewer_protocol_policy   = "redirect-to-https"
    compress                 = true
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer.id
  }

  ordered_cache_behavior {
    path_pattern             = "/videos/*"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    target_origin_id         = "api-alb-origin"
    viewer_protocol_policy   = "redirect-to-https"
    compress                 = true
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer.id
  }

  ordered_cache_behavior {
    path_pattern             = "/healthz"
    allowed_methods          = ["GET", "HEAD"]
    cached_methods           = ["GET", "HEAD"]
    target_origin_id         = "api-alb-origin"
    viewer_protocol_policy   = "redirect-to-https"
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer.id
  }

  ordered_cache_behavior {
    path_pattern             = "/readyz"
    allowed_methods          = ["GET", "HEAD"]
    cached_methods           = ["GET", "HEAD"]
    target_origin_id         = "api-alb-origin"
    viewer_protocol_policy   = "redirect-to-https"
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer.id
  }

  # The three 15 TTLs, ahead of the /processed/* catch-all because CloudFront takes the
  # first matching pattern and * matches across /.
  ordered_cache_behavior {
    path_pattern               = "/processed/*/master.m3u8"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    target_origin_id           = "processed-s3-origin"
    viewer_protocol_policy     = "redirect-to-https"
    compress                   = true
    cache_policy_id            = aws_cloudfront_cache_policy.master_manifest.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id
  }

  ordered_cache_behavior {
    path_pattern               = "/processed/*/playlist.m3u8"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    target_origin_id           = "processed-s3-origin"
    viewer_protocol_policy     = "redirect-to-https"
    compress                   = true
    cache_policy_id            = aws_cloudfront_cache_policy.variant_playlist.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id
  }

  # No compress on segments: MPEG-TS is already compressed, so gzip at the edge costs CPU
  # and returns nothing.
  ordered_cache_behavior {
    path_pattern               = "/processed/*.ts"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    target_origin_id           = "processed-s3-origin"
    viewer_protocol_policy     = "redirect-to-https"
    cache_policy_id            = aws_cloudfront_cache_policy.segments.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id
  }

  # Thumbnails: processed/{id}/thumbnails/cover.jpg and {second}.jpg match none of the three
  # patterns above, so without this they would fall through to the web bucket and 404.
  ordered_cache_behavior {
    path_pattern               = "/processed/*"
    allowed_methods            = ["GET", "HEAD", "OPTIONS"]
    cached_methods             = ["GET", "HEAD", "OPTIONS"]
    target_origin_id           = "processed-s3-origin"
    viewer_protocol_policy     = "redirect-to-https"
    compress                   = true
    cache_policy_id            = data.aws_cloudfront_cache_policy.caching_optimized.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id
  }

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD", "OPTIONS"]
    target_origin_id       = "web-s3-origin"
    viewer_protocol_policy = "redirect-to-https"
    compress               = true
    cache_policy_id        = data.aws_cloudfront_cache_policy.caching_optimized.id
  }

  # SPA fallback for the TanStack Router deep link /videos/$id.
  #
  # 403 ONLY, deliberately not 404. custom_error_response is distribution-wide, not
  # per-behaviour, so mapping 404 here would rewrite the API's own 404s -- GET and DELETE
  # /videos/{id} on an unknown id, per openapi.yaml -- into a 200 carrying index.html, and
  # api.ts's json() helper would try to parse HTML. 403 is safe because S3 with OAC returns
  # 403 AccessDenied (not 404) for a missing key, since the bucket policy grants GetObject
  # but not ListBucket, and openapi.yaml defines no 403 anywhere: 200, 201, 204, 400, 404,
  # 409, 500, 503.
  #
  # Two things break this, both worth knowing before changing either: granting ListBucket to
  # CloudFront makes S3 answer 404 instead, and adding a 403 to the API (authentication,
  # spec 17) would start serving the SPA shell for denied requests. Either one means moving
  # this to a CloudFront Function on the default behaviour, which is per-behaviour and does
  # not have this problem.
  custom_error_response {
    error_code            = 403
    response_code         = 200
    response_page_path    = "/index.html"
    error_caching_min_ttl = 0
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  # Uses the shared *.cloudfront.net certificate, which is what lets this work with no domain
  # and no ACM certificate. Swap for viewer_certificate.acm_certificate_arn + aliases when a
  # custom domain is wanted.
  viewer_certificate {
    cloudfront_default_certificate = true
  }

  tags = {
    Name        = "${var.project_name}-${var.environment}-cdn"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

Then append the web bucket's policy next to the processed one, at the bottom of the same file:

```hcl
resource "aws_s3_bucket_policy" "web" {
  bucket = var.web_bucket_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowCloudFrontServicePrincipalReadOnly"
        Effect    = "Allow"
        Principal = { Service = "cloudfront.amazonaws.com" }
        Action    = "s3:GetObject"
        Resource  = "${var.web_bucket_arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.this.arn
          }
        }
      }
    ]
  })
}
```

`s3:GetObject` only, no `s3:ListBucket` — that is what makes S3 answer 403 rather than 404 for a missing key, which the SPA fallback above depends on.

Finally, update the `ASSUMPTION` comment at the top of the file: the real layout is `processed/<video_id>/master.m3u8` and `processed/<video_id>/<rendition>/playlist.m3u8` (`apps/worker/pipeline.go` `objectKey`), not the bare `<video_id>/...` it currently claims, and the patterns above now carry that prefix.

Append to `infrastructure/terraform/modules/cloudfront/outputs.tf`:

```hcl
output "distribution_url" {
  description = "https:// URL of the distribution: the single entry point for the web app, the API, and HLS playback."
  value       = "https://${aws_cloudfront_distribution.this.domain_name}"
}
```

- [ ] **Step 7: Stop the ALB answering the open internet**

In `infrastructure/terraform/modules/alb/main.tf`, add the prefix-list lookup after the `terraform` block:

```hcl
# AWS publishes and maintains the origin-facing CloudFront ranges as a managed prefix list,
# so the ALB security group never needs a hardcoded CIDR list that goes stale.
data "aws_ec2_managed_prefix_list" "cloudfront_origin_facing" {
  name = "com.amazonaws.global.cloudfront.origin-facing"
}
```

and replace the `ingress` block of `aws_security_group.alb`:

```hcl
  # CloudFront is the only client: the distribution fronts the web app, the API, and the
  # processed assets on one hostname, so the ALB's own DNS name should not serve anyone.
  # This is not origin authentication -- the prefix list covers every CloudFront
  # distribution in every account. Closing that gap needs a shared secret origin header and
  # a check in the API; see the trade-offs at the top of Task 5.
  ingress {
    description     = "HTTP from CloudFront origin-facing ranges"
    from_port       = 80
    to_port         = 80
    protocol        = "tcp"
    prefix_list_ids = [data.aws_ec2_managed_prefix_list.cloudfront_origin_facing.id]
  }
```

Update the module's own top-of-file note too: replace the "production should terminate HTTPS at the ALB" comment with one that says CloudFront terminates TLS for viewers, this listener is the unencrypted CloudFront-to-origin hop, and a domain plus an ACM certificate is what closes it.

- [ ] **Step 8: Wire the environment**

In `environments/dev/main.tf`, add one argument to `module "s3"`:

```hcl
  web_bucket_name = "${local.project_name}-${local.environment}-web"
```

and replace the whole `module "cloudfront"` block with:

```hcl
module "cloudfront" {
  source = "../../modules/cloudfront"

  project_name = local.project_name
  environment  = local.environment

  processed_bucket_id                   = module.s3.processed_bucket_id
  processed_bucket_arn                  = module.s3.processed_bucket_arn
  processed_bucket_regional_domain_name = module.s3.processed_bucket_regional_domain_name

  web_bucket_id                   = module.s3.web_bucket_id
  web_bucket_arn                  = module.s3.web_bucket_arn
  web_bucket_regional_domain_name = module.s3.web_bucket_regional_domain_name

  alb_dns_name = module.alb.alb_dns_name

  cors_allowed_origins = var.cors_allowed_origins
  price_class          = var.cloudfront_price_class
}
```

Append to `environments/dev/outputs.tf`:

```hcl
output "app_url" {
  description = "The single entry point: web app at /, API at the openapi.yaml paths, HLS under /processed/."
  value       = module.cloudfront.distribution_url
}

output "public_asset_base_url" {
  description = "Value of PUBLIC_ASSET_BASE_URL handed to the API. Cross-plan contract 4 -- the API prepends this to the keys stored in the database."
  value       = "https://${module.cloudfront.distribution_domain_name}"
}

output "web_bucket_id" {
  description = "Bucket the deploy pipeline syncs the Vite build into."
  value       = module.s3.web_bucket_id
}

output "cloudfront_distribution_id" {
  description = "Distribution ID, needed to create invalidations after a web deploy."
  value       = module.cloudfront.distribution_id
}
```

- [ ] **Step 9: Validate**

```bash
cd infrastructure/terraform
terraform fmt -check -recursive
cd environments/dev
terraform init -backend=false
terraform validate
cd ../../../..
grep -rn 'cloudfront_oac_id' infrastructure/terraform
grep -c 'ordered_cache_behavior' infrastructure/terraform/modules/cloudfront/main.tf
```

Expected: `Success! The configuration is valid.`, the `cloudfront_oac_id` grep prints nothing, and the count is `8`.

- [ ] **Step 10: Assert the behaviour order and the API path set mechanically**

The ordering rules in this task are the kind that break silently, so check them rather than eyeballing:

```bash
grep -o 'path_pattern *= *"[^"]*"' infrastructure/terraform/modules/cloudfront/main.tf \
  | sed 's/.*= *//' | tr -d '"'
```

Expected, in exactly this order:

```
/videos
/videos/*
/healthz
/readyz
/processed/*/master.m3u8
/processed/*/playlist.m3u8
/processed/*.ts
/processed/*
```

If `/processed/*` appears before any of the three §15 patterns, thumbnails and segments share one TTL and §15 is violated. Then confirm the API set still matches the contract:

```bash
grep -E "^  /" docs/specifications/openapi.yaml
```

Expected: `/videos`, `/videos/{id}`, `/videos/{id}/complete`, `/healthz`, `/readyz` — five paths, covered by the four patterns above. If `openapi.yaml` ever grows a path outside `/videos`, `/healthz`, or `/readyz`, this behaviour list needs a matching entry or that route will be served the web bundle.

- [ ] **Step 11: Confirm no application code moved**

```bash
git status --short apps/
```

Expected: empty (`apps/web/dist/` is gitignored, so step 2's build does not show). If anything under `apps/` is modified, revert it: both contracts in this task's preamble say the move to CloudFront is configuration, not code.

- [ ] **Step 12: [AWS ONLY] Prove all three origins**

After Task 8's first deploy, with one video processed to `ready`:

```bash
cd infrastructure/terraform/environments/dev
APP="$(terraform output -raw app_url)"
BUCKET="$(terraform output -raw processed_bucket_id)"
ALB="$(terraform output -raw alb_dns_name)"
cd -
ID="<the ready video's id>"
```

The web app, including a deep link that only S3-404s-as-403 plus the fallback can serve:

```bash
curl -sI "$APP/" | head -1
curl -s "$APP/videos/$ID" | head -5
```

Expected: `HTTP/2 200`, and the deep link returns the `index.html` shell (a `<!doctype html>` line), not an XML `AccessDenied`.

The API, same origin, and — critically — a real 404 that the fallback did **not** swallow:

```bash
curl -s "$APP/healthz"; echo
curl -s "$APP/readyz"; echo
curl -si "$APP/videos/00000000-0000-0000-0000-000000000000" | head -1
```

Expected: `{"status":"ok"}`; `{"checks":{"database":"ok"},"status":"ok"}`; and `HTTP/2 404`. That last line is the one to look at hardest — if it is `200`, the `custom_error_response` is mapping 404 as well as 403 and the API contract is broken.

Playback, with the §15 TTLs and edge CORS:

```bash
curl -sI "$APP/processed/$ID/master.m3u8" | grep -i 'HTTP/\|access-control-allow-origin\|x-cache\|cache-control'
curl -sI "$APP/processed/$ID/master.m3u8" | grep -i 'x-cache'
curl -sI -H 'Origin: https://example.com' "$APP/processed/$ID/720/playlist.m3u8" | grep -i 'access-control-allow-origin'
curl -sI "$APP/processed/$ID/thumbnails/cover.jpg" | head -1
```

Expected: `200` with `access-control-allow-origin: *`; `Miss from cloudfront` then `Hit from cloudfront`; the allow-origin header on the variant playlist; and `200` for the thumbnail, which proves the `/processed/*` catch-all is behind the three §15 patterns rather than in front of them.

Both origins private, and the ALB no longer public:

```bash
curl -sI "https://$BUCKET.s3.amazonaws.com/processed/$ID/master.m3u8" | head -1
curl -s --max-time 10 "http://$ALB/healthz" ; echo "curl exit=$?"
```

Expected: `HTTP/1.1 403 Forbidden` from S3 — the bucket is reachable only through the OAC. And the direct ALB call times out (`curl exit=28`), because its security group now admits only CloudFront's ranges. A `200` there means the prefix-list ingress from step 7 did not apply.

Finally open `$APP` in a browser, upload a clip, and play it. Expected: no CORS error and no mixed-content warning in the console, and the network panel shows `/videos` calls going to the same hostname as the page.

- [ ] **Step 13: Commit**

```bash
git add infrastructure/terraform/modules/cloudfront infrastructure/terraform/modules/s3 \
  infrastructure/terraform/modules/alb infrastructure/terraform/environments/dev
git commit -m "feat: serve the web app, API, and HLS playback from one CloudFront distribution"
```

---
