# A fluxcd diff with dagger and k3s

This is a demo of how to use [dagger](https://dagger.io/) to run a [fluxcd](https://fluxcd.io/) diff on a [k3s](https://k3s.io/) cluster.

## Requirements

- [k3s](https://k3s.io/) cluster
- [dagger](https://dagger.io/) installed on your local machine
- [fluxcd](https://fluxcd.io/) installed on your local machine
- `GITHUB_TOKEN` environment variable set to a [GitHub personal access token](https://docs.github.com/en/github/authenticating-to-github/creating-a-personal-access-token)

## Usage

1. Make sure your repository contains changes that you want to apply to your cluster.
2. `go run main.go`

## References

This demo is based on [@marcosnils](https://github.com/marcosnils)'s suggested solution in https://github.com/dagger/dagger/issues/5292#issuecomment-1593750070
