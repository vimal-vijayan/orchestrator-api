# TerraformRun Controller - Quick Reference

## Installation

```bash
# Generate manifests
make manifests generate

# Install CRDs
make install

# Deploy controller to cluster
make deploy

# Or run locally for development
make run
```

## Sample TerraformRun

### Basic Example
```yaml
apiVersion: infra.essity.com/v1alpha1
kind: TfRun
metadata:
  name: my-infrastructure
spec:
  forProvider:
    credentialsSecretRef: tf-credentials
  source:
    module: https://github.com/myorg/terraform-modules.git
    ref: v1.0.0
    path: modules/vpc
  vars:
    region: us-east-1
    vpc_cidr: 10.0.0.0/16
```

### With Cloud Backend
```yaml
apiVersion: infra.essity.com/v1alpha1
kind: TfRun
metadata:
  name: my-infrastructure
spec:
  forProvider:
    credentialsSecretRef: tf-credentials
  source:
    module: https://github.com/myorg/terraform-modules.git
    ref: main
    path: modules/vpc
  backend:
    cloud:
      hostname: app.terraform.io
      organization: my-org
      workspace: production-vpc
  vars:
    region: us-east-1
```

### With Azure Storage Backend
```yaml
apiVersion: infra.essity.com/v1alpha1
kind: TfRun
metadata:
  name: my-infrastructure
spec:
  forProvider:
    credentialsSecretRef: azure-credentials
  source:
    module: https://github.com/myorg/terraform-modules.git
    ref: main
  backend:
    storageAccount:
      accountName: mytfstate
      containerName: tfstate
      key: production.tfstate
  vars:
    location: eastus
```

## Creating Credentials Secret

```bash
# Terraform Cloud token
kubectl create secret generic tf-credentials \
  --from-literal=token='your-terraform-cloud-token'

# Azure credentials
kubectl create secret generic azure-credentials \
  --from-literal=token='your-azure-token' \
  --from-literal=subscription-id='sub-id' \
  --from-literal=tenant-id='tenant-id'
```

## Common Operations

### Apply Infrastructure
```bash
# Create resource
kubectl apply -f my-tfrun.yaml

# Watch status
kubectl get tfrun my-infrastructure -w

# Check phase
kubectl get tfrun my-infrastructure -o jsonpath='{.status.phase}'
```

### View Job Logs
```bash
# Get Job name
kubectl get jobs -l terraformrun=my-infrastructure

# View git clone logs
kubectl logs job/<job-name> -c git-clone

# View terraform logs
kubectl logs job/<job-name> -c terraform

# Follow logs in real-time
kubectl logs -f job/<job-name> -c terraform
```

### Destroy Infrastructure
```bash
# Delete TerraformRun
kubectl delete tfrun my-infrastructure

# Watch destroy Job
kubectl get jobs -l terraformrun=my-infrastructure,job-type=destroy -w

# View destroy logs
kubectl logs job/<destroy-job-name> -c terraform
```

### Force Re-apply (Change Detection Override)
```bash
# Option 1: Change any var
kubectl patch tfrun my-infrastructure --type=merge -p '{"spec":{"vars":{"trigger":"'$(date +%s)'"}}}'

# Option 2: Delete and recreate
kubectl delete tfrun my-infrastructure --wait=true
kubectl apply -f my-tfrun.yaml
```

## Status Inspection

### Get Full Status
```bash
kubectl get tfrun my-infrastructure -o yaml | grep -A 20 "status:"
```

### Check Phase
```bash
kubectl get tfrun my-infrastructure -o jsonpath='{.status.phase}'
# Output: Running | Succeeded | Failed
```

### Check Conditions
```bash
kubectl get tfrun my-infrastructure -o jsonpath='{.status.conditions[*].type}'
# Output: Ready Applied

kubectl get tfrun my-infrastructure -o jsonpath='{.status.conditions[?(@.type=="Applied")]}'
```

### Check Last Run Time
```bash
kubectl get tfrun my-infrastructure -o jsonpath='{.status.lastRunTime}'
```

### Check Active Job
```bash
kubectl get tfrun my-infrastructure -o jsonpath='{.status.activeJobName}'
```

## Debugging

### Controller Not Working
```bash
# Check controller pods
kubectl get pods -n orchestrator-api-system

# View controller logs
kubectl logs -n orchestrator-api-system deployment/orchestrator-api-controller-manager -f

# Check RBAC
kubectl auth can-i create jobs --as=system:serviceaccount:orchestrator-api-system:orchestrator-api-controller-manager
```

### Job Fails to Start
```bash
# Describe Job
kubectl describe job <job-name>

# Check events
kubectl get events --sort-by='.lastTimestamp' | grep <job-name>

# Verify secret exists
kubectl get secret tf-credentials
```

### Git Clone Fails
```bash
# Check initContainer logs
kubectl logs job/<job-name> -c git-clone

# Common issues:
# - Invalid repository URL
# - Private repository without authentication
# - Invalid ref/branch
```

### Terraform Command Fails
```bash
# Check terraform logs
kubectl logs job/<job-name> -c terraform

# Common issues:
# - Missing credentials
# - Invalid backend configuration
# - Terraform syntax errors
# - Provider authentication issues
```

### Resource Stuck in Deletion
```bash
# Check destroy Job status
kubectl get jobs -l terraformrun=my-infrastructure,job-type=destroy

# View destroy Job logs
kubectl logs job/<destroy-job-name> -c terraform

# Force remove finalizer (use with caution!)
kubectl patch tfrun my-infrastructure --type json \
  -p='[{"op": "remove", "path": "/metadata/finalizers"}]'
```

## Monitoring

### List All TerraformRuns
```bash
kubectl get tfruns
kubectl get tfruns -A  # All namespaces
```

### Filter by Status
```bash
# Get all running
kubectl get tfruns -o json | jq -r '.items[] | select(.status.phase=="Running") | .metadata.name'

# Get all failed
kubectl get tfruns -o json | jq -r '.items[] | select(.status.phase=="Failed") | .metadata.name'

# Get all succeeded
kubectl get tfruns -o json | jq -r '.items[] | select(.status.phase=="Succeeded") | .metadata.name'
```

### Watch for Changes
```bash
# Watch specific resource
kubectl get tfrun my-infrastructure -w

# Watch all resources
kubectl get tfruns -w

# Watch with custom columns
kubectl get tfruns -w -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,JOB:.status.activeJobName,MESSAGE:.status.message
```

### Check Jobs
```bash
# List all Terraform Jobs
kubectl get jobs -l terraformrun

# List Jobs for specific TerraformRun
kubectl get jobs -l terraformrun=my-infrastructure

# List only apply Jobs
kubectl get jobs -l job-type=apply

# List only destroy Jobs
kubectl get jobs -l job-type=destroy
```

## Advanced Usage

### Custom Terraform Arguments
```yaml
spec:
  arguments:
    parallelism: "10"
    lock-timeout: "5m"
  vars:
    region: us-east-1
```

### Multiple Variables
```yaml
spec:
  vars:
    region: us-east-1
    environment: production
    vpc_cidr: 10.0.0.0/16
    enable_nat_gateway: "true"
    az_count: "3"
```

### Using Different Git Refs
```yaml
# Use branch
spec:
  source:
    module: https://github.com/myorg/modules.git
    ref: develop

# Use tag
spec:
  source:
    module: https://github.com/myorg/modules.git
    ref: v2.0.1

# Use commit SHA
spec:
  source:
    module: https://github.com/myorg/modules.git
    ref: abc123def456
```

## Troubleshooting Checklist

- [ ] Controller is running: `kubectl get pods -n orchestrator-api-system`
- [ ] CRDs are installed: `kubectl get crds | grep tfrun`
- [ ] RBAC is configured: Check ClusterRole and ClusterRoleBinding
- [ ] Credentials secret exists: `kubectl get secret <name>`
- [ ] Git repository is accessible
- [ ] Terraform module path is correct
- [ ] Backend configuration is valid
- [ ] Variables are properly formatted
- [ ] No active Job conflicts exist

## Performance Tips

1. **Reuse successful runs**: Don't delete/recreate unnecessarily
2. **Batch variable changes**: Update multiple vars in one patch
3. **Use specific Git refs**: Tags/SHAs are faster than branches
4. **Clean up old Jobs**: Set TTL or manually delete completed Jobs
5. **Monitor resource usage**: Jobs consume cluster resources

## Security Best Practices

1. **Never commit secrets**: Use Kubernetes Secrets
2. **Use RBAC**: Restrict who can create TerraformRuns
3. **Limit permissions**: Give controller minimal required permissions
4. **Use private Git repos**: With proper authentication
5. **Network policies**: Restrict Job network access
6. **Pod security**: Apply security contexts to Job pods
7. **Audit logging**: Enable audit logs for TerraformRun operations

## Cleanup

### Delete All TerraformRuns
```bash
# This will trigger destroy Jobs for all resources
kubectl delete tfruns --all

# Wait for all to be deleted
kubectl get tfruns -w
```

### Delete Completed Jobs
```bash
# Delete all succeeded Jobs
kubectl delete jobs -l terraformrun --field-selector status.successful=1

# Delete all failed Jobs
kubectl delete jobs -l terraformrun --field-selector status.failed=1
```

### Uninstall Controller
```bash
# Delete controller deployment
make undeploy

# Delete CRDs (caution: deletes all TerraformRun resources)
make uninstall
```

## Support

For issues and questions:
- Review controller logs
- Check Job logs
- Verify RBAC permissions
- Consult IMPLEMENTATION.md for detailed architecture
- Check Kubernetes events: `kubectl get events`
