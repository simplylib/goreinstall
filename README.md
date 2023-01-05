# goreinstall
A Go (golang) tool to update and/or reinstall binaries in GOBIN/GOROOT after a go version update

## Installing
```
go install github.com/simplylib/goreinstall@latest
```

## Usage
```
goreinstall reinstalls modules with new versions or when the go version is lower than the current one

Usage: goreinstall [flags] <package(s) ...>

Ex: goreinstall -a             // reinstall all binaries in GOBIN
Ex: goreinstall -a -u          // reinstall all binaries and update if needed
Ex: goreinstall goreinstall -u // reinstall goreinstall and update if needed

Flags:
  -a    reinstall all binaries in GOBIN (ex: after go version update)
  -c string
        name of binary to use instead of (go) for go commands (default "go")
  -e string
        list of binaries to exclude from running against ex: "goreinstall,gitsum"
  -f    forcefully reinstall binaries even if not required
  -l    list all binaries found in GOBIN with extra version information
  -t int
        max number of binaries to reinstall at once (default 8)
  -u    update binaries if there is an update available
  -v    be verbose about operations
```
