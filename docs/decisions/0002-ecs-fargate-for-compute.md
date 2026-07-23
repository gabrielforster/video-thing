# ADR-0002: ECS Fargate for Compute

## Status
Accepted

## Context
The platform runs two compute workloads with different scaling shapes:

- The **API service**: a steady-state, request-driven HTTP service that needs modest, predictable scaling and low-latency deploys.
- The **worker service**: a bursty, queue-driven fleet that must scale aggressively with SQS queue depth (including scaling toward zero when idle) to process transcoding jobs, and each job can run from tens of seconds to well over an hour depending on source video length and rendition count.

The team is small and does not want to take on Kubernetes-grade operational responsibility for an MVP. The design principles already committed to elsewhere in this platform are Infrastructure-as-Code and immutable deployments (build once, push an image to ECR, deploy that exact artifact). Whatever compute platform is chosen needs to compose cleanly with Terraform, ECR, and an SQS-depth-based scaling signal without requiring a dedicated platform/SRE function to operate day to day.

## Decision
Use **Amazon ECS on Fargate** for both the API service and the worker service. Each service is an ECS Service (API) or an ECS Service scaled via Application Auto Scaling target-tracking on SQS metrics (worker), running containers built from images pushed to ECR, provisioned entirely through Terraform.

## Alternatives Considered

- **Kubernetes / EKS** — The industry-standard choice for complex, multi-team, multi-tenant container platforms, with a huge ecosystem (Helm, operators, service mesh, HPA/KEDA for queue-based scaling). Rejected for this MVP because the operational overhead is disproportionate to the problem: running EKS well means managing cluster upgrades, node group AMIs or Fargate profiles, CNI/networking configuration, and a much larger Terraform/Helm surface area, none of which buys this platform anything at MVP scale (two services, one team, no multi-tenancy requirement). KEDA's queue-based autoscaling is genuinely elegant for the worker's use case, but that elegance doesn't offset the added cluster-management burden right now.

- **Lambda** — Attractive for the worker specifically because it's naturally event-driven (S3 event → SQS → Lambda) and scales instantly per-message with no idle cost. Rejected as a hard blocker: Lambda's 15-minute maximum execution time cannot accommodate transcoding longer source videos or multiple renditions sequentially, and ffmpeg pipelines need substantial scratch space and predictable local disk, which Lambda's ephemeral `/tmp` (capped, and reclaimed between invocations) and cold-start characteristics make awkward. Chunking a transcode job into sub-15-minute Lambda-sized units is possible in theory but adds substantial orchestration complexity (step functions, checkpointing partial encodes) for no clear MVP benefit over just running a long-lived container.

- **EC2 Auto Scaling Groups with self-managed ECS** — Cheaper at large, steady-state scale (reserved/spot pricing beats Fargate's per-task premium once utilization is high and predictable), and gives more control over instance types (e.g., for future GPU-accelerated encoding). Rejected for now because it reintroduces patching, AMI management, capacity planning, and bin-packing problems that Fargate abstracts away entirely. At MVP scale, with unpredictable and bursty load, the operational cost of managing EC2 capacity outweighs the per-task cost premium of Fargate.

## Consequences

### Positive
- Fargate's per-task model composes directly with the platform's scale-from-SQS-queue-depth-to-zero strategy: Application Auto Scaling watches `ApproximateNumberOfMessagesVisible` and adjusts desired task count without any node-level capacity management.
- No servers to patch, size, or bin-pack; the team's operational burden is limited to task definitions, IAM roles, and scaling policies — all expressible in Terraform alongside the rest of the infrastructure.
- Clean immutable-deployment story: build an image, push to ECR, update the ECS task definition revision, roll the service. No in-place mutation of running infrastructure.
- Per-task resource isolation means a large or misbehaving transcode job can't starve other tasks on the same host, which matters when job sizes vary widely.

### Negative / Tradeoffs
- Fargate carries a real per-vCPU/per-GB cost premium over equivalent EC2 capacity; at high, sustained utilization this becomes the more expensive option.
- Less control over the underlying instance than EC2 — no access to instance-store NVMe for scratch space, no GPU support at time of writing, no custom AMI/kernel tuning, which could matter if transcoding needs hardware acceleration later.
- Task startup latency (tens of seconds for image pull + container init) means true scale-from-zero has a cold-start tax; the worker fleet needs a floor above zero or an accepted latency hit for the first jobs after an idle period.
- Ephemeral storage per Fargate task is capped (configurable, but with upper limits), which constrains how large a source video plus working renditions can be before hitting disk limits.

## Notes
Revisit if the worker workload grows to need GPU-accelerated transcoding (Fargate has no GPU support as of this decision), or if sustained utilization becomes high and predictable enough that EC2/Spot capacity would meaningfully reduce cost — at that scale, a hybrid approach (Fargate for bursty/unpredictable load, EC2 for a steady-state baseline) or a move to EKS with Karpenter/KEDA could be justified by then having the operational headcount to support it.
