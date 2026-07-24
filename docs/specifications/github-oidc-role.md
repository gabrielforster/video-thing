# GitHub Actions OIDC Role for Terraform Provisioning

Trust and permission policies for the IAM role this repo's CI assumes via GitHub's OIDC
provider to run `terraform plan`/`apply` against AWS. Covers every resource type the
modules under `infrastructure/terraform/modules/` create: `networking`, `ecr`, `s3`, `sqs`,
`cloudfront`, `iam`, `logs`, `rds`, `alb`, `ecs`, `monitoring`.

Replace `ORG/REPO` and `ACCOUNT_ID` below with real values before applying.

## 1. OIDC identity provider (one-time, per AWS account)

```hcl
resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}
```

Skip if a `token.actions.githubusercontent.com` provider already exists in the account —
one provider per account, not per repo.

## 2. Trust policy (who can assume the role)

Scope `sub` to the repo and the branch/environment allowed to apply. Don't leave it as
`repo:ORG/REPO:*` — that lets any branch or PR assume the role.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::ACCOUNT_ID:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:ORG/REPO:ref:refs/heads/main"
        }
      }
    }
  ]
}
```

Per-environment deploys (dev/staging/production): use the `environment:` claim form
instead (`repo:ORG/REPO:environment:production`) and require a GitHub Environment with
protection rules on `production`, so `apply` against prod needs a reviewed gate, not just
a merge to `main`.

## 3. Permission policy

Everything below is provisioning-time (Terraform), not runtime — don't confuse with the
`iam` module's `ecs_task_execution_role` / `api_task_role` / `worker_task_role`, which are
what the *application* assumes at runtime and are already scoped tightly in
`infrastructure/terraform/modules/iam/main.tf`.

This role creates IAM roles/policies itself (the `iam` module) and can create ECS task
roles with arbitrary permissions — that's an inherent privilege-escalation surface for any
Terraform CI role, not specific to this repo. Mitigate with:
- resource-name scoping (`video-platform-*`) everywhere the action supports it,
- a permissions boundary attached to every role this CI creates (see 3.9),
- branch/environment-scoped trust policy (section 2) so only reviewed changes on `main`
  (or an approved environment) can assume the role at all.

### 3.1 Remote state backend (S3 + DynamoDB)

ADR-0005 calls for S3 + DynamoDB state locking, not yet wired up (`environments/dev/main.tf`
has `backend "s3" {}` commented out). Bootstrap this once, ideally with a *separate*,
one-time-use role/account-admin — not the recurring CI role — since state bucket/lock
table shouldn't be destroyable by routine applies:

```json
{
  "Sid": "TerraformState",
  "Effect": "Allow",
  "Action": [
    "s3:GetObject",
    "s3:PutObject",
    "s3:ListBucket"
  ],
  "Resource": [
    "arn:aws:s3:::video-platform-terraform-state",
    "arn:aws:s3:::video-platform-terraform-state/*"
  ]
},
{
  "Sid": "TerraformLock",
  "Effect": "Allow",
  "Action": [
    "dynamodb:GetItem",
    "dynamodb:PutItem",
    "dynamodb:DeleteItem"
  ],
  "Resource": "arn:aws:dynamodb:*:ACCOUNT_ID:table/video-platform-terraform-locks"
}
```

### 3.2 Networking (VPC, subnets, NAT, security groups)

No native resource-ARN scoping for most EC2 networking calls — IAM only supports
`Resource: "*"` for these. Rely on the trust-policy branch restriction (section 2) as the
real control here, not resource scoping.

```json
{
  "Sid": "Networking",
  "Effect": "Allow",
  "Action": [
    "ec2:CreateVpc", "ec2:DeleteVpc", "ec2:DescribeVpcs", "ec2:ModifyVpcAttribute",
    "ec2:CreateSubnet", "ec2:DeleteSubnet", "ec2:DescribeSubnets",
    "ec2:CreateInternetGateway", "ec2:DeleteInternetGateway", "ec2:AttachInternetGateway", "ec2:DetachInternetGateway", "ec2:DescribeInternetGateways",
    "ec2:CreateNatGateway", "ec2:DeleteNatGateway", "ec2:DescribeNatGateways",
    "ec2:AllocateAddress", "ec2:ReleaseAddress", "ec2:DescribeAddresses",
    "ec2:CreateRouteTable", "ec2:DeleteRouteTable", "ec2:CreateRoute", "ec2:DeleteRoute", "ec2:AssociateRouteTable", "ec2:DisassociateRouteTable", "ec2:DescribeRouteTables",
    "ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup", "ec2:AuthorizeSecurityGroupIngress", "ec2:AuthorizeSecurityGroupEgress", "ec2:RevokeSecurityGroupIngress", "ec2:RevokeSecurityGroupEgress", "ec2:DescribeSecurityGroups",
    "ec2:CreateTags", "ec2:DeleteTags", "ec2:DescribeTags",
    "ec2:DescribeAvailabilityZones"
  ],
  "Resource": "*"
}
```

### 3.3 ECR

```json
{
  "Sid": "Ecr",
  "Effect": "Allow",
  "Action": [
    "ecr:CreateRepository", "ecr:DeleteRepository", "ecr:DescribeRepositories",
    "ecr:SetRepositoryPolicy", "ecr:GetRepositoryPolicy", "ecr:DeleteRepositoryPolicy",
    "ecr:PutLifecyclePolicy", "ecr:GetLifecyclePolicy", "ecr:DeleteLifecyclePolicy",
    "ecr:TagResource", "ecr:ListTagsForResource"
  ],
  "Resource": "arn:aws:ecr:*:ACCOUNT_ID:repository/video-platform-*"
}
```

### 3.4 S3 (raw-uploads / processed-assets buckets)

```json
{
  "Sid": "S3Buckets",
  "Effect": "Allow",
  "Action": [
    "s3:CreateBucket", "s3:DeleteBucket", "s3:GetBucket*", "s3:PutBucket*",
    "s3:PutEncryptionConfiguration", "s3:PutBucketNotification", "s3:GetBucketNotification",
    "s3:PutBucketCORS", "s3:GetBucketCORS", "s3:PutLifecycleConfiguration", "s3:GetLifecycleConfiguration"
  ],
  "Resource": [
    "arn:aws:s3:::video-platform-*-raw-uploads",
    "arn:aws:s3:::video-platform-*-processed-assets"
  ]
}
```

### 3.5 SQS

```json
{
  "Sid": "Sqs",
  "Effect": "Allow",
  "Action": [
    "sqs:CreateQueue", "sqs:DeleteQueue", "sqs:GetQueueAttributes", "sqs:SetQueueAttributes",
    "sqs:GetQueueUrl", "sqs:TagQueue", "sqs:ListQueueTags"
  ],
  "Resource": "arn:aws:sqs:*:ACCOUNT_ID:video-platform-*"
}
```

### 3.6 CloudFront

CloudFront ARNs aren't known before creation and the API doesn't support resource-level
scoping for most of these calls — same `Resource: "*"` caveat as networking.

```json
{
  "Sid": "CloudFront",
  "Effect": "Allow",
  "Action": [
    "cloudfront:CreateDistribution", "cloudfront:UpdateDistribution", "cloudfront:DeleteDistribution",
    "cloudfront:GetDistribution", "cloudfront:GetDistributionConfig",
    "cloudfront:CreateOriginAccessControl", "cloudfront:GetOriginAccessControl", "cloudfront:DeleteOriginAccessControl", "cloudfront:UpdateOriginAccessControl",
    "cloudfront:TagResource", "cloudfront:ListTagsForResource"
  ],
  "Resource": "*"
}
```

### 3.7 IAM (roles/policies the `iam` module creates)

This is the privilege-escalation-sensitive block: the CI role can create other IAM roles.
Scope by name prefix and require every role it creates to carry the same permissions
boundary it's willing to assume (`iam:PassRole` restricted to that boundary), so this role
can't mint a role with more power than itself.

```json
{
  "Sid": "IamRolesScoped",
  "Effect": "Allow",
  "Action": [
    "iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:UpdateRole",
    "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy",
    "iam:AttachRolePolicy", "iam:DetachRolePolicy", "iam:ListAttachedRolePolicies", "iam:ListRolePolicies",
    "iam:TagRole", "iam:ListRoleTags"
  ],
  "Resource": "arn:aws:iam::ACCOUNT_ID:role/video-platform-*"
},
{
  "Sid": "IamPassRoleScoped",
  "Effect": "Allow",
  "Action": "iam:PassRole",
  "Resource": "arn:aws:iam::ACCOUNT_ID:role/video-platform-*",
  "Condition": {
    "StringEquals": { "iam:PassedToService": "ecs-tasks.amazonaws.com" }
  }
}
```

### 3.8 CloudWatch Logs

```json
{
  "Sid": "Logs",
  "Effect": "Allow",
  "Action": [
    "logs:CreateLogGroup", "logs:DeleteLogGroup", "logs:DescribeLogGroups",
    "logs:PutRetentionPolicy", "logs:TagResource", "logs:ListTagsForResource"
  ],
  "Resource": "arn:aws:logs:*:ACCOUNT_ID:log-group:/ecs/video-platform-*"
}
```

### 3.9 RDS + Secrets Manager

`rds` module creates a Postgres instance, subnet group, security group (covered by 3.2),
and a Secrets Manager secret holding the generated password (`modules/rds/main.tf`).

```json
{
  "Sid": "Rds",
  "Effect": "Allow",
  "Action": [
    "rds:CreateDBInstance", "rds:DeleteDBInstance", "rds:ModifyDBInstance", "rds:DescribeDBInstances",
    "rds:CreateDBSubnetGroup", "rds:DeleteDBSubnetGroup", "rds:DescribeDBSubnetGroups",
    "rds:AddTagsToResource", "rds:ListTagsForResource"
  ],
  "Resource": "arn:aws:rds:*:ACCOUNT_ID:*:video-platform-*"
},
{
  "Sid": "RdsSecrets",
  "Effect": "Allow",
  "Action": [
    "secretsmanager:CreateSecret", "secretsmanager:DeleteSecret", "secretsmanager:UpdateSecret",
    "secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue", "secretsmanager:DescribeSecret",
    "secretsmanager:TagResource"
  ],
  "Resource": "arn:aws:secretsmanager:*:ACCOUNT_ID:secret:video-platform-*"
}
```

### 3.10 ALB

```json
{
  "Sid": "Alb",
  "Effect": "Allow",
  "Action": [
    "elasticloadbalancing:CreateLoadBalancer", "elasticloadbalancing:DeleteLoadBalancer", "elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:ModifyLoadBalancerAttributes",
    "elasticloadbalancing:CreateTargetGroup", "elasticloadbalancing:DeleteTargetGroup", "elasticloadbalancing:DescribeTargetGroups", "elasticloadbalancing:ModifyTargetGroupAttributes",
    "elasticloadbalancing:CreateListener", "elasticloadbalancing:DeleteListener", "elasticloadbalancing:DescribeListeners", "elasticloadbalancing:ModifyListener",
    "elasticloadbalancing:AddTags", "elasticloadbalancing:DescribeTags"
  ],
  "Resource": "*"
}
```

ELB v2 doesn't support resource-level IAM policies for most create/describe calls, so this
is another `Resource: "*"` block — same trust-policy caveat as 3.2/3.6.

### 3.11 ECS (cluster, services, task definitions, autoscaling)

```json
{
  "Sid": "Ecs",
  "Effect": "Allow",
  "Action": [
    "ecs:CreateCluster", "ecs:DeleteCluster", "ecs:DescribeClusters",
    "ecs:RegisterTaskDefinition", "ecs:DeregisterTaskDefinition", "ecs:DescribeTaskDefinition",
    "ecs:CreateService", "ecs:DeleteService", "ecs:UpdateService", "ecs:DescribeServices",
    "ecs:TagResource", "ecs:ListTagsForResource"
  ],
  "Resource": "*"
},
{
  "Sid": "EcsAutoscaling",
  "Effect": "Allow",
  "Action": [
    "application-autoscaling:RegisterScalableTarget", "application-autoscaling:DeregisterScalableTarget",
    "application-autoscaling:PutScalingPolicy", "application-autoscaling:DeleteScalingPolicy",
    "application-autoscaling:DescribeScalableTargets", "application-autoscaling:DescribeScalingPolicies"
  ],
  "Resource": "*"
}
```

ECS task defs/services/clusters don't support resource-level scoping by name either.

### 3.12 Monitoring (CloudWatch dashboards/alarms, SNS read for the alarm topic)

```json
{
  "Sid": "Monitoring",
  "Effect": "Allow",
  "Action": [
    "cloudwatch:PutDashboard", "cloudwatch:GetDashboard", "cloudwatch:DeleteDashboards",
    "cloudwatch:PutMetricAlarm", "cloudwatch:DeleteAlarms", "cloudwatch:DescribeAlarms",
    "cloudwatch:TagResource"
  ],
  "Resource": "*"
},
{
  "Sid": "SnsAlarmTopicLookup",
  "Effect": "Allow",
  "Action": ["sns:GetTopicAttributes"],
  "Resource": "arn:aws:sns:*:ACCOUNT_ID:*"
}
```

(`monitoring` module takes `sns_alarm_topic_arn` as an input var — it reads/references an
existing topic, doesn't create one. If that changes, add `sns:CreateTopic`/`DeleteTopic`.)

## Notes

- Everything with `Resource: "*"` above is because AWS doesn't support resource-level IAM
  conditions for that action (EC2 networking, CloudFront, ELB, ECS, CloudWatch) — this is a
  real AWS limitation, not something narrowed further by better ARN scoping. The trust
  policy (section 2) is the actual control boundary for those.
- `staging`/`production` environment directories are reserved but not implemented (see
  README) — the `video-platform-*` prefix scoping above already covers them once they
  exist, no policy changes needed when they're added.
- Split into a separate read-only role (`plan`-only, no mutating actions) for PRs and a
  full apply role gated to `main`/environment approval, if PR-triggered `terraform plan` is
  wanted for review before merge.
