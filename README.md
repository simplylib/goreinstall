# goreinstall
A Go (golang) tool to update and/or reinstall binaries in GOBIN/GOROOT after a go version update

## Usage
```
goreinstall reinstalls modules with new versions or when the go version is lower than the current one

Usage: goreinstall [flags] <package(s) ...>

Ex: goreinstall -a             // reinstall all binaries in GOBIN
Ex: goreinstall -a -u          // reinstall all binaries and update if needed
Ex: goreinstall goreinstall -u // reinstall goreinstall and update if needed

Flags:  
  -a      reinstall all binaries in GOBIN (eX: after go version update)
  -e string
        list of binaries to exclude from running against ex: "goreinstall,gitsum"
  -l    list all binaries found in GOBIN with extra version information
  -t int
        max number of binaries to reinstall at once (default 12)
  -u    update binaries if there is an update available
  -v    be verbose about operations
```
