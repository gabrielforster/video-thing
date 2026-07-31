# Task 6: One-time AWS bootstrap — state bucket, OIDC provider, CI role

> Task 6 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 5](task-05-one-cloudfront-distribution-in-front-web.md) · Next: [Task 7](task-07-remote-state-backend-staging-production-environments.md)

---

**Files:**
- Create: `infrastructure/aws/github-actions-trust-policy.json`
- Create: `infrastructure/aws/github-actions-policy-core.json`
- Create: `infrastructure/aws/github-actions-policy-data.json`
- Create: `infrastructure/aws/github-actions-policy-compute.json`
- Create: `scripts/bootstrap-aws.sh`

**Why this is a script and not Terraform:** the role in question is the identity Terraform runs *as*. Managing it with the same Terraform that it authorises is a bootstrap loop, and `github-oidc-role.md` §3.1 explicitly says the state bucket and the CI identity should be created by a separate one-time admin identity so a routine `apply` cannot destroy them. The script is idempotent so it can be re-run to update the trust policy or a permission list.

**Three managed policies, not one inline policy:** an IAM role's *aggregate* inline policy size is 10,240 characters, which this policy set exceeds. Customer-managed policies are limited to 6,144 characters each with up to 10 attachable per role, so the statements are split by blast radius: `core` (state backend, VPC, logs, CloudWatch, IAM), `data` (S3, SQS, ECR repositories, CloudFront, Secrets Manager, RDS), `compute` (ALB, ECS, autoscaling, ECR push, `ecs run-task`).

**Interfaces:**
- Consumes: administrator credentials in the shell, `GITHUB_REPOSITORY` in `org/repo` form.
- Produces: the S3 state bucket, the `token.actions.githubusercontent.com` OIDC provider, the role `video-thing-github-actions` with the three policies attached. Prints the two GitHub repository variables Task 8 needs.

- [ ] **Step 1: Write the trust policy**

`infrastructure/aws/github-actions-trust-policy.json`. Two tokens, `__ACCOUNT_ID__` and `__ORG_REPO__`, are substituted by the bootstrap script — the file is committed as a template so the account ID never lands in git.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "GitHubActionsEnvironmentScoped",
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::__ACCOUNT_ID__:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": [
            "repo:__ORG_REPO__:environment:dev",
            "repo:__ORG_REPO__:environment:staging",
            "repo:__ORG_REPO__:environment:production"
          ]
        }
      }
    }
  ]
}
```

This is the `environment:` claim form that `github-oidc-role.md` §2 recommends for per-environment deploys, not the `ref:refs/heads/main` form in its example — the deploy workflow declares `environment:` on its job, so the token's `sub` carries the environment name, and gating `production` behind a GitHub Environment with required reviewers becomes a repository setting rather than a policy edit. It also sidesteps the fact that this repository's default branch is `master`, not `main`. Note the consequence: an environment name that does not exist as a GitHub Environment cannot assume the role at all, which is the intended failure mode.

- [ ] **Step 2: Write the three permission policies**

`infrastructure/aws/github-actions-policy-core.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "TerraformState",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"],
      "Resource": [
        "arn:aws:s3:::video-thing-terraform-state",
        "arn:aws:s3:::video-thing-terraform-state/*"
      ]
    },
    {
      "Sid": "Networking",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateVpc", "ec2:DeleteVpc", "ec2:DescribeVpcs", "ec2:DescribeVpcAttribute", "ec2:ModifyVpcAttribute",
        "ec2:CreateSubnet", "ec2:DeleteSubnet", "ec2:DescribeSubnets", "ec2:ModifySubnetAttribute",
        "ec2:CreateInternetGateway", "ec2:DeleteInternetGateway", "ec2:AttachInternetGateway", "ec2:DetachInternetGateway", "ec2:DescribeInternetGateways",
        "ec2:CreateNatGateway", "ec2:DeleteNatGateway", "ec2:DescribeNatGateways",
        "ec2:AllocateAddress", "ec2:ReleaseAddress", "ec2:DescribeAddresses", "ec2:DescribeAddressesAttribute",
        "ec2:CreateRouteTable", "ec2:DeleteRouteTable", "ec2:CreateRoute", "ec2:DeleteRoute", "ec2:AssociateRouteTable", "ec2:DisassociateRouteTable", "ec2:DescribeRouteTables",
        "ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup", "ec2:AuthorizeSecurityGroupIngress", "ec2:AuthorizeSecurityGroupEgress", "ec2:RevokeSecurityGroupIngress", "ec2:RevokeSecurityGroupEgress", "ec2:DescribeSecurityGroups", "ec2:DescribeSecurityGroupRules",
        "ec2:CreateTags", "ec2:DeleteTags", "ec2:DescribeTags",
        "ec2:DescribeAvailabilityZones", "ec2:DescribeNetworkInterfaces",
        "ec2:CreateVpcEndpoint", "ec2:DeleteVpcEndpoints", "ec2:DescribeVpcEndpoints", "ec2:ModifyVpcEndpoint",
        "ec2:DescribeManagedPrefixLists", "ec2:GetManagedPrefixListEntries", "ec2:DescribePrefixLists"
      ],
      "Resource": "*"
    },
    {
      "Sid": "LogGroupsScoped",
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup", "logs:DeleteLogGroup", "logs:PutRetentionPolicy",
        "logs:TagResource", "logs:UntagResource", "logs:ListTagsForResource",
        "logs:DescribeLogStreams", "logs:GetLogEvents", "logs:FilterLogEvents", "logs:StartLiveTail"
      ],
      "Resource": "arn:aws:logs:*:__ACCOUNT_ID__:log-group:/ecs/video-thing-*"
    },
    {
      "Sid": "LogGroupsList",
      "Effect": "Allow",
      "Action": ["logs:DescribeLogGroups"],
      "Resource": "*"
    },
    {
      "Sid": "Monitoring",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:PutDashboard", "cloudwatch:GetDashboard", "cloudwatch:DeleteDashboards", "cloudwatch:ListDashboards",
        "cloudwatch:PutMetricAlarm", "cloudwatch:DeleteAlarms", "cloudwatch:DescribeAlarms",
        "cloudwatch:TagResource", "cloudwatch:UntagResource", "cloudwatch:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "SnsAlarmTopicLookup",
      "Effect": "Allow",
      "Action": ["sns:GetTopicAttributes"],
      "Resource": "arn:aws:sns:*:__ACCOUNT_ID__:*"
    },
    {
      "Sid": "IamRolesScoped",
      "Effect": "Allow",
      "Action": [
        "iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:UpdateRole",
        "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy", "iam:ListRolePolicies",
        "iam:AttachRolePolicy", "iam:DetachRolePolicy", "iam:ListAttachedRolePolicies",
        "iam:TagRole", "iam:UntagRole", "iam:ListRoleTags"
      ],
      "Resource": "arn:aws:iam::__ACCOUNT_ID__:role/video-thing-*"
    },
    {
      "Sid": "IamPassRoleScoped",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": "arn:aws:iam::__ACCOUNT_ID__:role/video-thing-*",
      "Condition": {
        "StringEquals": { "iam:PassedToService": "ecs-tasks.amazonaws.com" }
      }
    },
    {
      "Sid": "IamServiceLinkedRoles",
      "Effect": "Allow",
      "Action": "iam:CreateServiceLinkedRole",
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "iam:AWSServiceName": [
            "ecs.amazonaws.com",
            "ecs.application-autoscaling.amazonaws.com",
            "elasticloadbalancing.amazonaws.com"
          ]
        }
      }
    }
  ]
}
```

`infrastructure/aws/github-actions-policy-data.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "S3Buckets",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket", "s3:DeleteBucket", "s3:ListBucket",
        "s3:GetBucket*", "s3:PutBucket*", "s3:DeleteBucketPolicy",
        "s3:PutEncryptionConfiguration", "s3:GetEncryptionConfiguration",
        "s3:PutLifecycleConfiguration", "s3:GetLifecycleConfiguration"
      ],
      "Resource": [
        "arn:aws:s3:::video-thing-*-raw-uploads",
        "arn:aws:s3:::video-thing-*-processed-assets",
        "arn:aws:s3:::video-thing-*-web"
      ]
    },
    {
      "Sid": "SqsScoped",
      "Effect": "Allow",
      "Action": [
        "sqs:CreateQueue", "sqs:DeleteQueue", "sqs:GetQueueAttributes", "sqs:SetQueueAttributes",
        "sqs:GetQueueUrl", "sqs:TagQueue", "sqs:UntagQueue", "sqs:ListQueueTags"
      ],
      "Resource": "arn:aws:sqs:*:__ACCOUNT_ID__:video-thing-*"
    },
    {
      "Sid": "SqsList",
      "Effect": "Allow",
      "Action": "sqs:ListQueues",
      "Resource": "*"
    },
    {
      "Sid": "EcrRepositories",
      "Effect": "Allow",
      "Action": [
        "ecr:CreateRepository", "ecr:DeleteRepository", "ecr:DescribeRepositories",
        "ecr:SetRepositoryPolicy", "ecr:GetRepositoryPolicy", "ecr:DeleteRepositoryPolicy",
        "ecr:PutLifecyclePolicy", "ecr:GetLifecyclePolicy", "ecr:DeleteLifecyclePolicy",
        "ecr:PutImageScanningConfiguration", "ecr:PutImageTagMutability",
        "ecr:TagResource", "ecr:UntagResource", "ecr:ListTagsForResource"
      ],
      "Resource": "arn:aws:ecr:*:__ACCOUNT_ID__:repository/video-thing-*"
    },
    {
      "Sid": "CloudFront",
      "Effect": "Allow",
      "Action": [
        "cloudfront:CreateDistribution", "cloudfront:UpdateDistribution", "cloudfront:DeleteDistribution",
        "cloudfront:GetDistribution", "cloudfront:GetDistributionConfig", "cloudfront:ListDistributions",
        "cloudfront:CreateOriginAccessControl", "cloudfront:GetOriginAccessControl", "cloudfront:GetOriginAccessControlConfig",
        "cloudfront:UpdateOriginAccessControl", "cloudfront:DeleteOriginAccessControl", "cloudfront:ListOriginAccessControls",
        "cloudfront:CreateCachePolicy", "cloudfront:GetCachePolicy", "cloudfront:GetCachePolicyConfig",
        "cloudfront:UpdateCachePolicy", "cloudfront:DeleteCachePolicy", "cloudfront:ListCachePolicies",
        "cloudfront:CreateResponseHeadersPolicy", "cloudfront:GetResponseHeadersPolicy", "cloudfront:GetResponseHeadersPolicyConfig",
        "cloudfront:UpdateResponseHeadersPolicy", "cloudfront:DeleteResponseHeadersPolicy", "cloudfront:ListResponseHeadersPolicies",
        "cloudfront:CreateInvalidation", "cloudfront:GetInvalidation",
        "cloudfront:TagResource", "cloudfront:UntagResource", "cloudfront:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "Secrets",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:CreateSecret", "secretsmanager:DeleteSecret", "secretsmanager:UpdateSecret",
        "secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue",
        "secretsmanager:GetResourcePolicy", "secretsmanager:ListSecretVersionIds",
        "secretsmanager:TagResource", "secretsmanager:UntagResource"
      ],
      "Resource": "arn:aws:secretsmanager:*:__ACCOUNT_ID__:secret:video-thing-*"
    },
    {
      "Sid": "Rds",
      "Effect": "Allow",
      "Action": [
        "rds:CreateDBInstance", "rds:DeleteDBInstance", "rds:ModifyDBInstance", "rds:DescribeDBInstances",
        "rds:CreateDBSubnetGroup", "rds:DeleteDBSubnetGroup", "rds:ModifyDBSubnetGroup", "rds:DescribeDBSubnetGroups",
        "rds:CreateDBSnapshot", "rds:DescribeDBSnapshots",
        "rds:AddTagsToResource", "rds:RemoveTagsFromResource", "rds:ListTagsForResource"
      ],
      "Resource": [
        "arn:aws:rds:*:__ACCOUNT_ID__:db:video-thing-*",
        "arn:aws:rds:*:__ACCOUNT_ID__:subgrp:video-thing-*",
        "arn:aws:rds:*:__ACCOUNT_ID__:snapshot:video-thing-*"
      ]
    },
    {
      "Sid": "RdsCatalogReads",
      "Effect": "Allow",
      "Action": ["rds:DescribeDBEngineVersions", "rds:DescribeOrderableDBInstanceOptions"],
      "Resource": "*"
    }
  ]
}
```

`infrastructure/aws/github-actions-policy-compute.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Alb",
      "Effect": "Allow",
      "Action": [
        "elasticloadbalancing:CreateLoadBalancer", "elasticloadbalancing:DeleteLoadBalancer",
        "elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeLoadBalancerAttributes",
        "elasticloadbalancing:ModifyLoadBalancerAttributes", "elasticloadbalancing:SetSecurityGroups", "elasticloadbalancing:SetSubnets",
        "elasticloadbalancing:CreateTargetGroup", "elasticloadbalancing:DeleteTargetGroup",
        "elasticloadbalancing:DescribeTargetGroups", "elasticloadbalancing:DescribeTargetGroupAttributes",
        "elasticloadbalancing:ModifyTargetGroup", "elasticloadbalancing:ModifyTargetGroupAttributes", "elasticloadbalancing:DescribeTargetHealth",
        "elasticloadbalancing:CreateListener", "elasticloadbalancing:DeleteListener",
        "elasticloadbalancing:DescribeListeners", "elasticloadbalancing:ModifyListener",
        "elasticloadbalancing:AddTags", "elasticloadbalancing:RemoveTags", "elasticloadbalancing:DescribeTags"
      ],
      "Resource": "*"
    },
    {
      "Sid": "Ecs",
      "Effect": "Allow",
      "Action": [
        "ecs:CreateCluster", "ecs:DeleteCluster", "ecs:DescribeClusters", "ecs:UpdateClusterSettings", "ecs:PutClusterCapacityProviders",
        "ecs:RegisterTaskDefinition", "ecs:DeregisterTaskDefinition", "ecs:DescribeTaskDefinition", "ecs:ListTaskDefinitions",
        "ecs:CreateService", "ecs:DeleteService", "ecs:UpdateService", "ecs:DescribeServices", "ecs:ListServices",
        "ecs:RunTask", "ecs:StopTask", "ecs:DescribeTasks", "ecs:ListTasks",
        "ecs:TagResource", "ecs:UntagResource", "ecs:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "EcsAutoscaling",
      "Effect": "Allow",
      "Action": [
        "application-autoscaling:RegisterScalableTarget", "application-autoscaling:DeregisterScalableTarget",
        "application-autoscaling:PutScalingPolicy", "application-autoscaling:DeleteScalingPolicy",
        "application-autoscaling:DescribeScalableTargets", "application-autoscaling:DescribeScalingPolicies",
        "application-autoscaling:TagResource", "application-autoscaling:UntagResource", "application-autoscaling:ListTagsForResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "EcrAuth",
      "Effect": "Allow",
      "Action": "ecr:GetAuthorizationToken",
      "Resource": "*"
    },
    {
      "Sid": "EcrPush",
      "Effect": "Allow",
      "Action": [
        "ecr:BatchCheckLayerAvailability", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
        "ecr:CompleteLayerUpload", "ecr:PutImage",
        "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer", "ecr:DescribeImages", "ecr:ListImages"
      ],
      "Resource": "arn:aws:ecr:*:__ACCOUNT_ID__:repository/video-thing-*"
    },
    {
      "Sid": "WebBucketSync",
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:PutObjectAcl", "s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::video-thing-*-web/*"
    },
    {
      "Sid": "WebBucketList",
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": "arn:aws:s3:::video-thing-*-web"
    }
  ]
}
```

Every `"Resource": "*"` above is an AWS limitation, not laziness: EC2 networking, CloudFront, ELBv2, ECS, and CloudWatch do not support resource-level IAM conditions for their create/describe calls. The trust policy in step 1 is the real control boundary for those, exactly as `github-oidc-role.md` "Notes" states.

- [ ] **Step 3: Validate the JSON before it reaches IAM**

```bash
for f in infrastructure/aws/*.json; do
  jq -e . "$f" >/dev/null && echo "ok $f"
  chars="$(jq -c . "$f" | tr -d ' \n' | wc -c)"
  echo "   $chars characters (managed-policy limit is 6144)"
done
```

Expected: `ok` for all four files, and every character count under 6144. If one is over, split its largest statement into a second policy file and add it to the loop in the bootstrap script.

- [ ] **Step 4: Write `scripts/bootstrap-aws.sh`**

```bash
#!/usr/bin/env bash
# One-time, run by an account administrator -- NOT by CI. Creates the Terraform state
# bucket, the GitHub OIDC provider, and the role CI assumes. Idempotent: re-run it to
# update the trust policy or a permission list.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GITHUB_REPOSITORY="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required, in org/repo form}"
AWS_REGION="${AWS_REGION:-us-east-1}"
STATE_BUCKET="${STATE_BUCKET:-video-thing-terraform-state}"
ROLE_NAME="${ROLE_NAME:-video-thing-github-actions}"

for bin in aws jq sed; do
    command -v "$bin" >/dev/null || { echo "missing required binary: $bin" >&2; exit 1; }
done

ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
echo "==> account $ACCOUNT_ID, region $AWS_REGION, repo $GITHUB_REPOSITORY"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

render() {
    sed -e "s#__ACCOUNT_ID__#$ACCOUNT_ID#g" -e "s#__ORG_REPO__#$GITHUB_REPOSITORY#g" "$1" >"$2"
}

if aws s3api head-bucket --bucket "$STATE_BUCKET" >/dev/null 2>&1; then
    echo "==> state bucket $STATE_BUCKET already exists"
else
    echo "==> creating state bucket $STATE_BUCKET"
    if [ "$AWS_REGION" = "us-east-1" ]; then
        aws s3api create-bucket --bucket "$STATE_BUCKET" --region "$AWS_REGION" >/dev/null
    else
        aws s3api create-bucket --bucket "$STATE_BUCKET" --region "$AWS_REGION" \
            --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null
    fi
fi

# Versioning is not optional: it is the only way back from a corrupted or truncated state file.
aws s3api put-bucket-versioning --bucket "$STATE_BUCKET" \
    --versioning-configuration Status=Enabled
aws s3api put-bucket-encryption --bucket "$STATE_BUCKET" \
    --server-side-encryption-configuration \
    '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
aws s3api put-public-access-block --bucket "$STATE_BUCKET" \
    --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

PROVIDER_ARN="arn:aws:iam::$ACCOUNT_ID:oidc-provider/token.actions.githubusercontent.com"
if aws iam get-open-id-connect-provider --open-id-connect-provider-arn "$PROVIDER_ARN" >/dev/null 2>&1; then
    echo "==> OIDC provider already exists (one per account, not per repo)"
else
    echo "==> creating the GitHub OIDC provider"
    aws iam create-open-id-connect-provider \
        --url https://token.actions.githubusercontent.com \
        --client-id-list sts.amazonaws.com \
        --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1 >/dev/null
fi

render infrastructure/aws/github-actions-trust-policy.json "$WORK/trust.json"
if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
    echo "==> updating the trust policy on $ROLE_NAME"
    aws iam update-assume-role-policy --role-name "$ROLE_NAME" \
        --policy-document "file://$WORK/trust.json"
else
    echo "==> creating role $ROLE_NAME"
    aws iam create-role --role-name "$ROLE_NAME" \
        --description "GitHub Actions OIDC role for video-thing terraform and deploys" \
        --max-session-duration 3600 \
        --assume-role-policy-document "file://$WORK/trust.json" >/dev/null
fi

for part in core data compute; do
    POLICY_NAME="video-thing-github-actions-$part"
    POLICY_ARN="arn:aws:iam::$ACCOUNT_ID:policy/$POLICY_NAME"
    render "infrastructure/aws/github-actions-policy-$part.json" "$WORK/$part.json"

    if aws iam get-policy --policy-arn "$POLICY_ARN" >/dev/null 2>&1; then
        echo "==> new default version for $POLICY_NAME"
        # IAM keeps at most 5 versions; prune the oldest non-default before adding one.
        OLDEST="$(aws iam list-policy-versions --policy-arn "$POLICY_ARN" \
            --query 'sort_by(Versions[?IsDefaultVersion==`false`], &CreateDate)[0].VersionId' \
            --output text)"
        COUNT="$(aws iam list-policy-versions --policy-arn "$POLICY_ARN" \
            --query 'length(Versions)' --output text)"
        if [ "$COUNT" -ge 5 ] && [ "$OLDEST" != "None" ]; then
            aws iam delete-policy-version --policy-arn "$POLICY_ARN" --version-id "$OLDEST"
        fi
        aws iam create-policy-version --policy-arn "$POLICY_ARN" \
            --policy-document "file://$WORK/$part.json" --set-as-default >/dev/null
    else
        echo "==> creating $POLICY_NAME"
        aws iam create-policy --policy-name "$POLICY_NAME" \
            --policy-document "file://$WORK/$part.json" >/dev/null
    fi

    aws iam attach-role-policy --role-name "$ROLE_NAME" --policy-arn "$POLICY_ARN"
done

cat <<EOF

OK: bootstrap complete.

Set these GitHub *repository variables* (Settings > Secrets and variables > Actions > Variables):
  AWS_ROLE_ARN = arn:aws:iam::$ACCOUNT_ID:role/$ROLE_NAME
  AWS_REGION   = $AWS_REGION

Then create GitHub Environments named dev, staging, and production, and put required
reviewers on production.

No GitHub *secret* is needed. Nothing this pipeline consumes is confidential: the role ARN
is not a credential, and the database password is generated by Terraform and read from
Secrets Manager by the ECS agent, so it never enters the repository or a workflow log.
EOF
```

- [ ] **Step 5: [AWS ONLY] Run it and verify**

```bash
chmod +x scripts/bootstrap-aws.sh
GITHUB_REPOSITORY=gabrielforster/video-thing AWS_REGION=us-east-1 ./scripts/bootstrap-aws.sh
```

Expected: the final block prints the two variable values. Verify:

```bash
aws iam get-role --role-name video-thing-github-actions \
  --query 'Role.AssumeRolePolicyDocument.Statement[0].Condition' --output json
aws iam list-attached-role-policies --role-name video-thing-github-actions \
  --query 'AttachedPolicies[].PolicyName' --output text
aws s3api get-bucket-versioning --bucket video-thing-terraform-state
```

Expected: the condition lists the three `repo:.../environment:...` subjects; the three policy names `video-thing-github-actions-core`, `-data`, `-compute`; `"Status": "Enabled"`.

Then re-run the script once. Expected: the same final block, no errors — idempotency confirmed.

- [ ] **Step 6: Commit**

```bash
git add infrastructure/aws scripts/bootstrap-aws.sh
git commit -m "feat: bootstrap the terraform state bucket and the GitHub OIDC deploy role"
```

---
