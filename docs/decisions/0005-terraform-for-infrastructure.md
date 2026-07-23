# ADR-0005: Terraform for Infrastructure

## Status
Accepted

## Context
The platform provisions a non-trivial amount of AWS infrastructure — networking (VPC, subnets, security groups), ECS (clusters, services, task definitions), RDS (Postgres), S3 (upload and processed-asset buckets), CloudFront, SQS, IAM roles/policies, CloudWatch logging, and monitoring/alerting — and needs to do so consistently across at least dev, staging, and production environments. The platform's stated design principles include Infrastructure-as-Code and Immutable Deployments, which rules out ad hoc console changes as an acceptable long-term practice. The team is small, so whatever tool is chosen needs to be learnable quickly, have mature first-class support for the AWS services in use, and support clean module reuse across environments so the same networking/ECS/RDS patterns aren't hand-copied three times.

## Decision
Use **Terraform** for all AWS provisioning, organized as reusable modules per concern — `networking`, `ecs`, `rds`, `s3`, `cloudfront`, `sqs`, `iam`, `logs`, and `monitoring` — instantiated per environment (dev/staging/production) with environment-specific variable files. State is managed via a remote backend (S3 for state storage with DynamoDB for locking) so applies are safe to run from multiple machines/CI without state corruption.

## Alternatives Considered

- **AWS CDK** — Offers genuine type safety and a much better authoring experience than raw templates: infrastructure is defined in a real programming language (TypeScript/Python) with autocomplete, refactoring tools, and unit-testable constructs. Rejected for this platform because it ties infrastructure-as-code to a specific language runtime (adding a build/synth step and language-version management to the infra pipeline), and because CDK's generated CloudFormation can be genuinely difficult to debug when something goes wrong at the CloudFormation layer — engineers end up needing to understand both CDK's abstraction and the CloudFormation it emits. Terraform's plan/apply model and HCL, while less expressive than a general-purpose language, is more directly debuggable: what you read in the plan output is close to what will actually change.

- **Raw CloudFormation** — Rejected primarily on ergonomics: CloudFormation's YAML/JSON templates are verbose, its module reuse story (nested stacks, or more recently modules) is weaker and clunkier than Terraform's module system, and its error messages for failed stack operations are frequently unhelpful relative to Terraform's plan diffs. There's no meaningful advantage to raw CloudFormation over Terraform for a multi-service AWS platform unless the team specifically wants to avoid any third-party tooling in favor of AWS-native-only tooling, which isn't a stated constraint here.

- **Pulumi** — Shares CDK's core appeal (real programming languages, testable infrastructure code) with arguably a cleaner state and provider model than CDK, and would let the team write infra in Go, matching the language choice for the backend services (ADR-0001). Rejected mainly on ecosystem maturity and convention: Terraform's AWS provider is more battle-tested, has broader community module coverage, and is simply more standard in this problem domain — meaning more available reference architectures, more Stack Overflow/community troubleshooting history, and easier hiring for infra-specific skills. Pulumi is a reasonable choice and not clearly worse technically, but Terraform's ecosystem maturity tips the decision for a small team that needs to move fast without reinventing infra patterns from scratch.

- **Manual console changes / ClickOps** — Explicitly rejected, not seriously considered as a viable path. This directly contradicts the platform's stated Infrastructure-as-Code and Immutable Deployments principles: manual changes produce drift that no tooling tracks, aren't repeatable across dev/staging/production, and leave no reviewable diff or audit trail for what changed and why. Any manual change made for expediency (e.g., a quick console tweak during an incident) needs to be reflected back into Terraform promptly or it becomes invisible technical debt.

## Consequences

### Positive
- Terraform's AWS provider has comprehensive, mature coverage of every service this platform uses (ECS, RDS, S3, CloudFront, SQS, IAM), so no resource type requires falling back to a provisioner or manual step.
- Module-per-concern structure (`networking`, `ecs`, `rds`, `s3`, `cloudfront`, `sqs`, `iam`, `logs`, `monitoring`) lets dev/staging/production share the same tested module code with only variable differences, reducing environment drift and duplicated logic.
- `terraform plan` gives a reviewable diff before any change is applied, which fits naturally into a PR-based review workflow for infrastructure changes, mirroring how application code is reviewed.
- Large community and reference-architecture base means common patterns (VPC layouts, ECS service definitions, CloudFront + OAC setups) rarely need to be designed from scratch.

### Negative / Tradeoffs
- HCL is a domain-specific language with real limits — conditional logic, loops, and dynamic resource generation are possible but often awkward compared to a general-purpose language (CDK/Pulumi's advantage), which can make highly dynamic infrastructure patterns clunkier to express.
- Remote state management (S3 + DynamoDB locking, or Terraform Cloud) is an operational requirement, not optional — losing or corrupting state, or two people applying concurrently without locking, is a real failure mode that needs to be set up correctly from day one rather than added later.
- Module boundaries need real discipline to keep clean; without care, modules can become either too rigid (forcing awkward variable overrides) or too loose (duplicating logic across environments anyway).
- Terraform version and provider version upgrades occasionally introduce breaking changes to resource schemas, requiring migration work that a fully managed IaC service might handle differently.

## Notes
Revisit if the team's infra needs grow complex enough that HCL's limitations (dynamic resource generation, complex conditionals) become a genuine productivity bottleneck — at that point CDK or Pulumi's general-purpose-language approach becomes more attractive. Revisit the state backend choice (S3 + DynamoDB vs. Terraform Cloud/Enterprise) if the team grows enough that state locking conflicts or the lack of built-in policy/RBAC around applies becomes a recurring pain point.
