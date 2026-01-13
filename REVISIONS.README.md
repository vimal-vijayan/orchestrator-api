## First revision (alphaV1): Job-based execution

It’s the fastest way to ship an MVP, it stays Kubernetes-native, and you can evolve it later into a runner/queue or Terraform Cloud model without throwing away everything.

What v1 should look like
	•	1 CRD: TerraformRun (or TerraformWorkspace)
	•	1 Controller: watches CRs, creates Jobs, updates status, handles finalizer
	•	1 Job template:
	•	initContainer: git clone (or download artifact)
	•	terraform container: init → plan → apply (or destroy)
	•	shared workspace volume: emptyDir/PVC
	•	Remote backend for state + locking (Terraform Cloud or AzureRM/S3)

### Hardening points for v1
	1.	One active Job per CR

	    •	Use a Job label like terraformrun=<name> and check for existing active jobs before creating a new one.

	2.	Status conditions

	    •	Pending, Running, Succeeded, Failed
	    •	Store jobName, lastRunTime, runID/hash in .status

	3.	Spec hash to avoid reruns

	    •	Compute hash of .spec and store in .status.lastAppliedHash
	    •	Only run when hash changes (or when user sets spec.forceRun=true)

	4.	Finalizer for delete

	    •	On deletionTimestamp: create destroy Job → wait success → remove finalizer

	5.	Basic safety controls

	    •	Pod resource limits + namespace quota guidance
	    •	RBAC: controller can create Jobs only in allowed namespaces
	    •	Secrets mounted as files (avoid env + logs leaking)

⸻

#### Why this is the best first revision

	•	Minimum moving parts (no queue, no workflow engine, no external CI required)
	•	Clear separation: controller orchestrates, Job executes
	•	Easy to debug with kubectl logs job/...
	•	The same CRD/status model can later trigger:
	•	Argo Workflows/Tekton instead of Jobs, or
	•	Terraform Cloud runs instead of in-cluster execution

⸻

#### What to consider as v2 (upgrade path)

	•	Replace git-clone initContainer with OCI artifacts or a cached runner
	•	Add log shipping + run history
	•	Add approval gates (Argo/Tekton or TFC policies)
	•	Add multi-tenancy (per-team runners, tighter RBAC, isolation)
