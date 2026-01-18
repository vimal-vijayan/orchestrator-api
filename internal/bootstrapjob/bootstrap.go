package bootstrapjob

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	"infra.essity.com/orchstrator-api/internal/engine"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	tofuEngine = "opentofu"
	tfEngine   = "terraform"
	// terragrunt is not yet implemented
	tguntEngine = "terragrunt"

	workdDir = "/workdDir"

	labelJobType   = "job-type"
	jobTypeApply   = "apply"
	jobTypeDestroy = "destroy"

	jobBackoffLimit = 3
	// ttlSuccessDefault = int32(3600)  // 1 hour
	// ttlFailureDefault = int32(86400) // 1 day
	// job TTL defaults (in seconds)
	ttlSuccessDefault = int32(120) // 2 minutes
	ttlFailureDefault = int32(120) // 2 minutes
)

type BuildJobInterface interface {
	BuildJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, jobType string) (*batchv1.Job, error)
}

func (b *BootstrapJob) BuildJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, jobType string) (*batchv1.Job, error) {
	logger := log.FromContext(ctx)
	logger.Info("Building bootstrap job", "jobType", jobType)

	// compute a short hash for unique job name
	specHash, err := b.computeSpecHash(tfRun)
	if err != nil {
		logger.Error(err, "failed to compute spec hash", "tfRun", tfRun.Name)
		return nil, fmt.Errorf("failed to compute spec hash: %w", err)
	}

	jobName := fmt.Sprintf("%s-%s-%s", tfRun.Name, jobType, specHash)
	logger.Info("computed job name", "jobName", jobName)

	// Get the engine command
	tfEngine := engine.ForEngine(b.EngineType, []string{})
	tfCommand := tfEngine.Command(jobType)

	// Get engine image
	// FIXME: make engine image configurable
	engineImage := getEngineImage(b.EngineType)
	logger.V(1).Info("using engine image", "engineImage", engineImage)

	// Build variables for the job
	logger.V(1).Info("building variables as environment variables for", "tfCommand", tfCommand)
	logger.V(1).Info("building environment variables", "varCount", len(tfRun.Spec.Vars))
	envVars := append([]corev1.EnvVar{}, b.buildEnvVars(*tfRun)...)

	// add backend configuration as environment variables
	logger.V(1).Info("building backend configuration as environment variables")
	backendEnvVars, err := b.cloudBackend(*tfRun)
	if err != nil {
		logger.Error(err, "failed to build backend environment variables")
		return nil, err
	}
	envVars = append(envVars, backendEnvVars...)

	// Get git credentials
	gitToken, err := b.getGitCredentials(ctx, tfRun, tfRun.Spec.Source.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}

	repoURL := tfRun.Spec.Source.Module
	if gitToken != "" {
		// Convert https://github.com/user/repo.git to https://token@github.com/user/repo.git
		//FIXME: use alternative methods for git clone, instead of embedding token in URL
		if len(repoURL) > 8 && repoURL[:8] == "https://" {
			repoURL = fmt.Sprintf("https://%s@%s", gitToken, repoURL[8:])
			logger.Info("Using authenticated git clone for private repository")
		} else {
			logger.Info("Git credentials available but repository URL is not HTTPS, using public clone")
		}
	} else {
		logger.Info("No git credentials found, using public repository clone")
	}

	// Build git clone command
	var gitCloneCmd string
	if tfRun.Spec.Source.Ref != "" {
		// Clone the repository and then checkout the specific ref
		// This handles branches, tags, and commit SHAs uniformly
		gitCloneCmd = fmt.Sprintf("git clone --depth 5 %s %s && cd %s && git fetch --depth 5 origin %s && git checkout %s",
			repoURL, workdDir, workdDir, tfRun.Spec.Source.Ref, tfRun.Spec.Source.Ref)
	} else {
		// Clone default branch
		gitCloneCmd = fmt.Sprintf("git clone --depth 5 %s %s", repoURL, workdDir)
	}

	job := b.buildJobTemplate(tfRun, jobName, jobType, tfCommand, envVars, gitCloneCmd)
	logger.Info("Successfully built bootstrap job", "jobName", job.Name)

	// Log the built command for debugging
	// tofuCmd := fmt.Sprintf("%s %s", tfCommand, strings.Join(envVarsToString(envVars), " "))
	// logger.V(1).Info("built tofu command", "jobType", jobType, "tofuCmd", tofuCmd)

	return job, nil
}

func getEngineImage(engineType string) string {
	switch engineType {
	case tofuEngine:
		return "ghcr.io/opentofu/opentofu:latest"
	case tfEngine:
		return "hashicorp/terraform:latest"
	default:
		return "ghcr.io/opentofu/opentofu:latest"
	}
}

func (b *BootstrapJob) buildJobTemplate(tfRun *infrav1alpha1.TfRun, jobName string, jobType string, tfCommand string, envVars []corev1.EnvVar, gitCloneCmd string) *batchv1.Job {

	backoffLimit := int32(jobBackoffLimit)

	ttlSeconds := getTTL("JOB_TTL_SUCCESS", ttlSuccessDefault)
	if jobType == jobTypeDestroy {
		ttlSeconds = getTTL("JOB_TTL_DESTROY", ttlFailureDefault)
	}

	// set workding dir for opentofu/terraform
	workingDir := workdDir
	if tfRun.Spec.Source.Path != "" {
		workingDir = fmt.Sprintf("%s/%s", workdDir, tfRun.Spec.Source.Path)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: tfRun.Namespace,
			Labels: map[string]string{
				b.EngineType: tfRun.Name,
				labelJobType: jobType,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "git-clone",
							Image:   "alpine/git:latest",
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{gitCloneCmd},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: workingDir,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "opentofu",
							Image:   "ghcr.io/opentofu/opentofu:latest",
							Command: []string{"/bin/sh", "-c"},
							// Args:    []string{tfCommand},
							Args:       []string{"echo 'Running command: tofu plan'"},
							WorkingDir: workingDir,
							Env:        envVars,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: workingDir,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "workspace",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	return job
}

func getTTL(env string, defaultTTL int32) int32 {
	logger := log.Log.WithName("getTTL")
	logger.V(1).Info("Retrieving TTL from environment", "env", env, "defaultTTL", defaultTTL)
	if val := os.Getenv(env); val != "" {
		ttl, err := strconv.Atoi(val)
		if err == nil && ttl >= 600 {
			return int32(ttl)
		}
		logger.V(1).Error(err, "Invalid TTL value, using default", "value", val)
	}
	return defaultTTL
}

func (b *BootstrapJob) buildEnvVars(tfRun infrav1alpha1.TfRun) []corev1.EnvVar {
	envVars := []corev1.EnvVar{}
	for key, val := range tfRun.Spec.Vars {
		if val != nil {
			varValue := string(val.Raw)
			envVars = append(envVars, corev1.EnvVar{
				Name:  fmt.Sprintf("TF_VAR_%s", key),
				Value: varValue,
			})
		}
	}
	return envVars
}

func (b *BootstrapJob) cloudBackend(tfRun infrav1alpha1.TfRun) ([]corev1.EnvVar, error) {
	cloudBackend := tfRun.Spec.Backend.Cloud

	if cloudBackend == nil {
		return []corev1.EnvVar{}, fmt.Errorf("the cloud backend does not exist")
	}

	envVars := []corev1.EnvVar{}
	envVars = append(envVars,
		corev1.EnvVar{Name: "TF_CLOUD_HOSTNAME", Value: cloudBackend.Hostname},
		corev1.EnvVar{Name: "TF_CLOUD_ORGANIZATION", Value: cloudBackend.Organization},
		corev1.EnvVar{Name: "TF_WORKSPACE", Value: cloudBackend.Workspace},
	)

	// add cloud token from secret
	if tfRun.Spec.ForProvider.CredentialsSecretRef != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: fmt.Sprintf("TF_TOKEN_%s", strings.ReplaceAll(cloudBackend.Hostname, ".", "_")),
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: tfRun.Spec.ForProvider.CredentialsSecretRef,
					},
					Key: "token",
				},
			},
		})
	}
	return envVars, nil
}

// func (b *BootstrapJob) s3Backend(backend infrav1alpha1.TfBackend) []corev1.EnvVar {
// 	return []corev1.EnvVar{}
// }

// func (b *BootstrapJob) storageAccountBackend(backend infrav1alpha1.TfBackend) []corev1.EnvVar {
// 	return []corev1.EnvVar{}
// }

// computeSpecHash computes a hash of the TfRun spec to detect changes
func (b *BootstrapJob) computeSpecHash(tfRun *infrav1alpha1.TfRun) (string, error) {
	// Create a struct with fields that should trigger a new run
	hashInput := struct {
		Module    string
		Ref       string
		Path      string
		Vars      map[string]*apiextensionsv1.JSON
		Arguments map[string]string
		Backend   infrav1alpha1.TfBackend
	}{
		Module:    tfRun.Spec.Source.Module,
		Ref:       tfRun.Spec.Source.Ref,
		Path:      tfRun.Spec.Source.Path,
		Vars:      tfRun.Spec.Vars,
		Arguments: tfRun.Spec.Arguments,
		Backend:   tfRun.Spec.Backend,
	}

	data, err := json.Marshal(hashInput)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec: %w", err)
	}

	hash := sha256.Sum256(data)
	hashStr := fmt.Sprintf("%x", hash[:8])
	return hashStr, nil
}

func (b *BootstrapJob) getGitCredentials(ctx context.Context, tfRun *infrav1alpha1.TfRun, secretName string) (string, error) {
	logger := log.FromContext(ctx)

	// use default secret name if not provided
	if secretName == "" {
		secretName = "git-credentials"
		logger.V(1).Info("no git credentials secret name provided, using default", "secretName", secretName)
	}

	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      secretName,
		Namespace: tfRun.Namespace,
	}

	if err := b.K8s.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get git credentials secret", "secretName", secretName)
			return "", fmt.Errorf("failed to get git credentials secret: %w", err)
		}
		logger.Error(err, "error retrieving git credentials secret", "secretName", secretName)
		return "", fmt.Errorf("error retrieving git credentials secret: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		logger.Error(fmt.Errorf("token key not found"), "token key not found in git credentials secret", "secretName", secretName)
		return "", fmt.Errorf("token key not found in git credentials secret %s", secretName)
	}

	if len(token) == 0 {
		logger.Error(fmt.Errorf("empty token"), "git credentials token is empty in secret", "secretName", secretName)
		return "", fmt.Errorf("git credentials token is empty in secret")
	}

	logger.V(1).Info("successfully retrieved git credentials token from secret", "secretName", secretName)
	return string(token), nil
}

type BootstrapJob struct {
	K8s        client.Client
	EngineType string
	EngineArgs []string
}

func ForEngine(k8s client.Client, engine string, args []string) (BuildJobInterface, error) {
	switch engine {
	case tofuEngine:
		return &BootstrapJob{
			K8s:        k8s,
			EngineType: tofuEngine,
			EngineArgs: args,
		}, nil
	case tfEngine:
		return &BootstrapJob{
			K8s:        k8s,
			EngineType: tfEngine,
			EngineArgs: args,
		}, nil
	case tguntEngine:
		return nil, fmt.Errorf("terragrunt engine is not yet implemented")
	default:
		return &BootstrapJob{
			K8s:        k8s,
			EngineType: tofuEngine,
			EngineArgs: args,
		}, fmt.Errorf("unsupported engine: defaulting to opentofu for engine: %s", engine)
	}
}
