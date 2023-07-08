package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
)

var (
	fluxBootstrapCmd = `\
		bootstrap github \
		--owner=shaked \
		--repository=fluxcd-test \
		--branch=main \
		--path=clusters/tests
	`
	githubToken = os.Getenv("GITHUB_TOKEN")
)

func NewK8sInstance(ctx context.Context, client *dagger.Client) *K8sInstance {
	return &K8sInstance{
		ctx:         ctx,
		client:      client,
		container:   nil,
		configCache: client.CacheVolume("k3s_config"),
	}
}

type K8sInstance struct {
	ctx         context.Context
	client      *dagger.Client
	container   *dagger.Container
	configCache *dagger.CacheVolume
}

func (k *K8sInstance) start() error {
	// create k3s service container
	k3s := k.client.Pipeline("k3s init").Container().
		From("rancher/k3s").
		WithMountedCache("/etc/rancher/k3s", k.configCache).
		WithMountedTemp("/etc/lib/cni").
		WithMountedTemp("/var/lib/kubelet").
		WithMountedTemp("/var/lib/rancher/k3s").
		WithMountedTemp("/var/log").
		WithEntrypoint([]string{"sh", "-c"}).
		WithExec([]string{"k3s server --bind-address $(ip route | grep src | awk '{print $NF}') --disable traefik --disable metrics-server"}, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}).
		WithExposedPort(6443)

	kubectlImage := k.client.Container().From("bitnami/kubectl")
	helmImage := k.client.Container().From("alpine/helm")
	fluxcdImage := k.client.Container().From("ghcr.io/fluxcd/flux-cli:v2.0.0-rc.5")

	// the git repository containing code for the binary to be built
	gitUrl := fmt.Sprintf("https://oauth2:%s@github.com/Shaked/fluxcd-test.git", githubToken)
	gitRepo := k.client.Git(gitUrl).
		Branch("diff").
		Tree()

	k.container = k.client.Container().
		From("cgr.dev/chainguard/wolfi-base:latest").
		// From("alpine:latest").
		WithFile("/usr/local/bin/kubectl", kubectlImage.File("/opt/bitnami/kubectl/bin/kubectl")).
		WithFile("/usr/local/bin/helm", helmImage.File("/usr/bin/helm")).
		WithFile("/usr/local/bin/flux", fluxcdImage.File("/usr/local/bin/flux")).
		WithExec([]string{"apk", "add", "--no-cache", "curl", "jq", "openssh-client", "git"}).
		WithMountedCache("/cache/k3s", k.configCache).
		WithServiceBinding("k3s", k3s).
		WithEnvVariable("CACHE", time.Now().String()).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithEnvVariable("GITHUB_TOKEN", githubToken).
		WithUser("root").
		WithExec([]string{"mkdir", "-p", "/.kube"}).
		WithExec([]string{"cp", "/cache/k3s/k3s.yaml", "/.kube/config"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithUser("root").
		WithDirectory("/src", gitRepo).
		WithWorkdir("/tmp").
		// WithDirectory("/host", k.client.Directory()).
		WithEntrypoint([]string{"sh", "-c"})

	if err := k.waitForNodes(); err != nil {
		return fmt.Errorf("failed to start k8s: %v", err)
	}
	return nil
}

func (k *K8sInstance) kubectl(command string) (string, error) {
	return k.exec("kubectl", fmt.Sprintf("kubectl %v", command))
}

func (k *K8sInstance) helm(command string) (string, error) {
	return k.exec("helm", fmt.Sprintf("helm %v", command))
}

func (k *K8sInstance) flux(command string) (string, error) {
	return k.exec("flux", fmt.Sprintf("flux %v", command))
}

func (k *K8sInstance) git(command string) (string, error) {
	return k.exec("git", fmt.Sprintf("git %v", command))
}

func (k *K8sInstance) exec(name, command string) (string, error) {
	return k.container.Pipeline(name).Pipeline(command).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{command}).
		Stdout(k.ctx)
}

func (k *K8sInstance) waitForNodes() (err error) {
	maxRetries := 5
	retryBackoff := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetNodes, err := k.kubectl("get nodes -o wide")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch nodes: %v", err))
			continue
		}
		if strings.Contains(kubectlGetNodes, "Ready") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetNodes)
	}
	return fmt.Errorf("k8s took too long to start")
}

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	k8s := NewK8sInstance(ctx, client)
	if err = k8s.start(); err != nil {
		panic(err)
	}

	_, err = k8s.flux(fluxBootstrapCmd)

	if err != nil {
		panic(err)
	}

	fluxWaitApps, err := k8s.kubectl(`wait kustomization/apps --for=condition=ready --timeout=5m -n flux-system`)
	if err != nil {
		panic(err)
	}
	fmt.Println(fluxWaitApps)

	hr, err := k8s.kubectl("get hr -A -o wide")
	if err != nil {
		panic(err)
	}
	fmt.Println(hr)

	pods, err := k8s.kubectl("get pods -A -o wide")
	if err != nil {
		panic(err)
	}
	fmt.Println(pods)

	helm, err := k8s.helm("ls -A")
	if err != nil {
		panic(err)
	}
	fmt.Println(helm)

	ls, err := k8s.exec("ls", fmt.Sprintf("ls -la %v", "/src"))
	if err != nil {
		panic(err)
	}

	fmt.Println("ls", ls)

	hostDir := "/src"

	fluxInfraDiff, err := k8s.flux(
		fmt.Sprintf(
			`diff kustomization infra-custom \
			--path %s/infra`,
			hostDir,
		),
	)

	if err != nil {
		log.Println("infra-custom error, failed for error: ", err)
		log.Println(k8s.container.ExitCode(k8s.ctx))
	}
	log.Println(fluxInfraDiff)

	fluxAppsDiff, err := k8s.flux(
		fmt.Sprintf(
			`diff kustomization apps \
			--path %s/apps`,
			hostDir,
		),
	)

	if err != nil {
		log.Println("apps error, failed for error: ", err)
		log.Println(k8s.container.ExitCode(k8s.ctx))
	}
	log.Println(fluxAppsDiff)

	fluxSystemDiff, err := k8s.flux(fmt.Sprintf(
		`diff kustomization flux-system \
			--path %s/clusters/tests`,
		hostDir,
	))
	if err != nil {
		log.Println("flux-system error, failed for error: ", err)
		log.Println(k8s.container.ExitCode(k8s.ctx))
		// panic(err)
	}
	log.Println(fluxSystemDiff)
}
