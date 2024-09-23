package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"container-registry.com/harbor-satellite/ci/internal/dagger"
)

// Attach would attach a docker as a service to the container provided.
func (m *HarborSatellite) Attach(ctx context.Context, container *dagger.Container, dockerVersion string) (*dagger.Container, error) {
	dockerd := m.Service(dockerVersion)

	dockerd, err := dockerd.Start(ctx)
	if err != nil {
		return nil, err
	}

	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "tcp",
	})
	if err != nil {
		return nil, err
	}

	return container.
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", dockerHost), nil
}

// Get a Service container running dockerd
func (m *HarborSatellite) Service(
	// +optional
	// +default="24.0"
	dockerVersion string,
) *dagger.Service {
	port := 2375
	return dag.Container().
		From(fmt.Sprintf("docker:%s", dockerVersion)).
		WithMountedCache(
			"/var/lib/docker",
			dag.CacheVolume(dockerVersion+"-docker-lib"),
			dagger.ContainerWithMountedCacheOpts{
				Sharing: dagger.Private,
			}).
		WithExposedPort(port).
		WithExec([]string{
			"dockerd",
			"--host=tcp://0.0.0.0:2375",
			"--host=unix:///var/run/docker.sock",
			"--tls=false",
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		AsService()
}

// Would build the project with the source provided. The name should be the name of the project.
func (m *HarborSatellite) build(source *dagger.Directory, component string) *dagger.Directory {
	fmt.Printf("Building %s\n", component)

	gooses := []string{"linux", "darwin"}
	goarches := []string{"amd64", "arm64"}
	binaryName := component // base component for the binary

	// create empty directory to put build artifacts
	outputs := dag.Directory()

	golang := dag.Container().
		From(DEFAULT_GO).
		WithDirectory(PROJ_MOUNT, source).
		WithWorkdir(PROJ_MOUNT)
	for _, goos := range gooses {
		for _, goarch := range goarches {
			// create the full binary component with OS and architecture
			outputBinary := fmt.Sprintf("%s/%s-%s-%s", component, binaryName, goos, goarch)

			// build artifact with specified binary component
			build := golang.
				WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
				WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
				WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
				WithEnvVariable("GOCACHE", "/go/build-cache").
				WithEnvVariable("GOOS", goos).
				WithEnvVariable("GOARCH", goarch).
				WithExec([]string{"go", "build", "-o", outputBinary})

			// add build to outputs
			outputs = outputs.WithDirectory(component, build.Directory(component))
		}
	}

	// return build directory
	return outputs
}

// PrepareForRelease prepares the repository for a release by creating a new tag. The default release type is "patch".
func (m *HarborSatellite) get_release_tag(ctx context.Context, git_container *dagger.Container, source *dagger.Directory, name string,
	// +optional
	// +default="patch"
	release_type string) (string, error) {
	/// This would get the last tag that was created. Empty string if no tag was created.
	getTagsOutput, err := git_container.
		WithExec([]string{
			"/bin/sh", "-c",
			fmt.Sprintf(`git tag --list "v*%s" | sort -V | tail -n 1`, name),
		}).
		Stdout(ctx)

	if err != nil {
		slog.Error("Failed to get tags: ", err, ".")
		slog.Error("Get Tags Output:", getTagsOutput, ".")
		return getTagsOutput, err
	}

	slog.Info("Get Tags Output:", getTagsOutput, ".")
	latest_tag := strings.TrimSpace(getTagsOutput)
	new_tag, err := generateNewTag(latest_tag, name, release_type)
	if err != nil {
		slog.Error("Failed to generate new tag: ", err.Error(), ".")
		return "", err
	}

	return new_tag, nil
}

func generateNewTag(latestTag, suffix, release_type string) (string, error) {
	if latestTag == "" {
		// If the latest tag is empty, this is the first release
		return fmt.Sprintf("v0.0.1-%s", suffix), nil
	}
	versionWithoutSuffix := strings.TrimSuffix(latestTag, fmt.Sprintf("-%s", suffix))
	versionWithoutSuffix = strings.TrimPrefix(versionWithoutSuffix, "v")
	fmt.Println("Version without suffix: ", versionWithoutSuffix)
	parts := strings.Split(versionWithoutSuffix, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		slog.Error("Failed to convert major version to integer: ", err.Error(), ".")
		return "", err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		slog.Error("Failed to convert minor version to integer: ", err.Error(), ".")
		return "", err
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		slog.Error("Failed to convert patch version to integer: ", err.Error(), ".")
		return "", err
	}
	// Increment the version according to the release type
	switch release_type {
	case "major":
		major++
	case "minor":
		minor++
	case "patch":
		patch++
	}
	newVersion := fmt.Sprintf("v%d.%d.%d-%s", major, minor, patch, suffix)

	return newVersion, nil
}

func (m *HarborSatellite) getPathToReleaser(name string) (string, error) {
	switch name {
	case "satellite":
		return ".goreleaser.yaml", nil
	case "ground-control":
		return "ground-control/.goreleaser.yaml", nil
	default:
		return "", fmt.Errorf("unknown name: %s", name)
	}
}
