# TerraformRun Controller Implementation

## Overview

This implementation provides a production-quality Kubernetes controller for managing Terraform operations through a custom resource called `TerraformRun`. The controller follows Kubebuilder best practices and implements a robust state machine with proper lifecycle management.

## Architecture

### Controller Responsibilities
The controller **does not execute Terraform directly**. Instead, it:
1. Creates and manages Kubernetes Jobs that run Terraform
2. Tracks Job lifecycle and updates TerraformRun status
3. Implements idempotency through spec hash comparison
4. Manages finalizers for proper cleanup with destroy Jobs

### Job Structure
Each Terraform Job consists of:
- **initContainer (git-clone)**: Clones the Git repository into a shared workspace volume
- **terraform container**: Runs `terraform init/plan/apply` or `destroy`
- **Shared Volume**: EmptyDir volume for workspace data

## State Machine

### Normal Lifecycle (Apply)

```
TerraformRun Created
        ↓
   [Pending] ← Initial state
        ↓
   Compute spec hash
        ↓
   Hash changed OR first run?
   ├─ No → Skip (idempotent)
   └─ Yes → Create Job
        ↓
   [Running] ← Job active
        ↓
   Job completed?
   ├─ Succeeded → [Succeeded]
   └─ Failed → [Failed]
```

### Deletion Lifecycle (Destroy)

```
TerraformRun Deleted
        ↓
   Finalizer present?
        ↓
   Create destroy Job
        ↓
   [Running] ← Destroy Job active
        ↓
   Job completed?
   ├─ Succeeded → Remove finalizer → Delete resource
   └─ Failed → Remove finalizer (prevent stuck resources)
```

## Status Fields

The controller maintains comprehensive status information:

```go
type TfRunStatus struct {
    // ObservedGeneration tracks the CR generation
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
    
    // Phase: Pending | Running | Succeeded | Failed
    Phase string `json:"phase,omitempty"`
    
    // ActiveJobName is the currently running Job
    ActiveJobName string `json:"activeJobName,omitempty"`
    
    // LastSpecHash prevents unnecessary re-runs
    LastSpecHash string `json:"lastSpecHash,omitempty"`
    
    // LastRunTime tracks the last successful run
    LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`
    
    // Message provides human-readable status
    Message string `json:"message,omitempty"`
    
    // Conditions follow Kubernetes conventions
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```

## Key Features

### 1. Idempotency
The controller computes a SHA256 hash of:
- Repository URL
- Git reference
- Path within repository
- Variables
- Backend configuration

If the hash matches `lastSpecHash` and the last run succeeded, **no new Job is created**.

### 2. Safety Rules
- **Only one active Job per TerraformRun**: Prevents race conditions
- **backoffLimit = 0**: Jobs don't retry automatically
- **Unique Job names**: `<cr-name>-<type>-<hash>`
- **Owner references**: Jobs are automatically cleaned up when CR is deleted
- **Labels**: All Jobs labeled with `terraformrun=<cr-name>` for easy tracking

### 3. Finalizer Management
- Finalizer: `terraform-operator.io/finalizer`
- Added on first reconciliation
- Triggers destroy Job on deletion
- Removed only after successful destroy (or on failure to prevent stuck resources)

### 4. Condition Management
The controller maintains three condition types:
- **Ready**: Overall resource health
- **Applied**: Terraform apply status
- **Destroyed**: Terraform destroy status

## Controller Functions

### Core Functions

#### `Reconcile()`
Main reconciliation loop:
1. Fetch TerraformRun resource
2. Ensure finalizer is present
3. Handle deletion if `DeletionTimestamp` is set
4. Compute current spec hash
5. Check for active Jobs and update status
6. Create new apply Job if needed

#### `ensureFinalizer()`
Adds the finalizer if not present using `controllerutil`.

#### `handleDeletion()`
Manages the deletion lifecycle:
- Creates destroy Job
- Tracks destroy Job status
- Removes finalizer on completion

#### `computeSpecHash()`
Computes SHA256 hash of relevant spec fields for change detection.

#### `buildTerraformJob()`
Constructs Kubernetes Job manifests with:
- InitContainer for git clone
- Terraform container with appropriate commands
- Environment variables from vars and backend config
- Shared workspace volume

#### Helper Functions
- `isJobActive()`: Checks if `job.Status.Active > 0`
- `isJobSucceeded()`: Checks if `job.Status.Succeeded > 0`
- `isJobFailed()`: Checks if `job.Status.Failed > 0`
- `updateStatus()`: Updates status and requeues after 10 seconds

## RBAC Permissions

The controller requires:

```go
// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
```

## Example TerraformRun Resource

```yaml
apiVersion: infra.essity.com/v1alpha1
kind: TfRun
metadata:
  name: tfrun-sample
spec:
  forProvider:
    credentialsSecretRef: tf-credentials
  source:
    module: https://github.com/example/terraform-modules.git
    ref: main
    path: modules/network
  backend:
    cloud:
      hostname: essity.scalr.io
      organization: my-org
      workspace: my-workspace
  vars:
    region: "eu-west-1"
    environment: "production"
```

## Job Configuration

### Apply Job
```bash
# initContainer (git-clone)
git clone <module> /workspace && cd /workspace && git checkout <ref>

# terraform container
cd /workspace/<path>
terraform init && terraform plan && terraform apply -auto-approve
```

### Destroy Job
```bash
# initContainer (git-clone)
git clone <module> /workspace && cd /workspace && git checkout <ref>

# terraform container
cd /workspace/<path>
terraform destroy -auto-approve
```

## Environment Variables

The controller automatically configures:

### Terraform Variables
```bash
TF_VAR_<key>=<value>  # From spec.vars
```

### Backend Configuration
```bash
TF_CLOUD_HOSTNAME=<hostname>
TF_CLOUD_ORGANIZATION=<organization>
TF_WORKSPACE=<workspace>
```

### Credentials
```bash
TERRAFORM_TOKEN=<from-secret>  # From spec.forProvider.credentialsSecretRef
```

## Testing the Controller

### 1. Generate CRDs and RBAC
```bash
make manifests
make generate
```

### 2. Install CRDs
```bash
make install
```

### 3. Run Controller
```bash
make run
```

### 4. Create Sample Resource
```bash
kubectl apply -f config/samples/infra_v1alpha1_tfrun.yaml
```

### 5. Check Status
```bash
kubectl get tfrun tfrun-sample -o yaml
kubectl get jobs -l terraformrun=tfrun-sample
kubectl logs <job-pod-name> -c terraform
```

### 6. Test Deletion
```bash
kubectl delete tfrun tfrun-sample
# Watch for destroy Job creation
kubectl get jobs -l terraformrun=tfrun-sample,job-type=destroy -w
```

## Monitoring

### Check Status
```bash
# Get TerraformRun status
kubectl get tfrun <name> -o jsonpath='{.status}'

# Watch phase changes
kubectl get tfrun <name> -w

# Check conditions
kubectl get tfrun <name> -o jsonpath='{.status.conditions}'
```

### Debug Jobs
```bash
# List all Jobs for a TerraformRun
kubectl get jobs -l terraformrun=<name>

# Get Job logs
kubectl logs job/<job-name> -c git-clone      # InitContainer logs
kubectl logs job/<job-name> -c terraform      # Terraform logs

# Describe Job for events
kubectl describe job <job-name>
```

## Best Practices

1. **Idempotency**: The controller automatically prevents duplicate runs
2. **Resource Cleanup**: Use finalizers for proper destroy lifecycle
3. **Status Updates**: Always update status to reflect current state
4. **Error Handling**: Failed Jobs are tracked and don't block deletion
5. **Requeue Strategy**: Requeue every 10 seconds during active operations
6. **Job Naming**: Include hash for uniqueness and traceability
7. **Owner References**: Ensure Jobs are owned by TerraformRun for automatic cleanup

## Limitations and Future Enhancements

### Current Limitations
- No support for private Git repositories (add SSH key support)
- No Terraform output capture in status
- No support for Terraform workspaces
- Fixed container images (should be configurable)

### Potential Enhancements
1. **Terraform Output**: Capture and store outputs in status
2. **Plan-only Mode**: Support plan without apply
3. **Approval Workflow**: Manual approval before apply
4. **Private Git**: SSH key or token-based authentication
5. **Custom Images**: Configurable Terraform and Git images
6. **Resource Drift Detection**: Periodic plan to detect drift
7. **Multi-workspace Support**: Manage multiple Terraform workspaces
8. **Notification Webhooks**: Send status updates to external systems

## Troubleshooting

### Job Not Created
- Check if spec hash matches lastSpecHash
- Verify controller logs: `kubectl logs deployment/orchestrator-api-controller-manager -n orchestrator-api-system`

### Job Fails Immediately
- Check Job logs: `kubectl logs job/<job-name> -c terraform`
- Verify credentials secret exists
- Check Git repository accessibility

### Resource Stuck in Deletion
- Check destroy Job status: `kubectl get jobs -l job-type=destroy`
- Manually remove finalizer if needed: `kubectl patch tfrun <name> --type json -p='[{"op": "remove", "path": "/metadata/finalizers"}]'`

### Status Not Updating
- Verify controller is running: `kubectl get pods -n orchestrator-api-system`
- Check RBAC permissions
- Review controller logs for errors

## Code Quality

This implementation follows:
- ✅ Kubernetes controller best practices
- ✅ Kubebuilder conventions
- ✅ Proper error handling
- ✅ Comprehensive logging
- ✅ Idempotent reconciliation
- ✅ Production-ready patterns
- ✅ Clean, readable code with comments
- ✅ Type safety with Go
- ✅ Proper status management

## References

- [Kubebuilder Book](https://book.kubebuilder.io/)
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [Kubernetes API Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
- [Terraform Documentation](https://www.terraform.io/docs)
