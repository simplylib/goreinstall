package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/simplylib/errgroup"
	"github.com/simplylib/multierror"
	"github.com/simplylib/ucheck/modproxy"
	"golang.org/x/mod/semver"
)

type goEnvVars struct {
	GoBin     string `json:"GOBIN"`
	GoPath    string `json:"GOPATH"`
	GoVersion string `json:"GOVERSION"`
}

func getGoEnv(ctx context.Context) (goEnvVars, error) {
	cmd := exec.CommandContext(ctx, "go", "env", "-json")
	cmd.Stderr = os.Stderr

	stdoutBuf := &bytes.Buffer{}
	cmd.Stdout = stdoutBuf

	err := cmd.Run()
	if err != nil {
		return goEnvVars{}, fmt.Errorf("could not run (go env -json) due to error (%w)", err)
	}

	goEnv := goEnvVars{}
	err = json.Unmarshal(stdoutBuf.Bytes(), &goEnv)
	if err != nil {
		return goEnvVars{}, fmt.Errorf("could not parse JSON from (go env -json) due to error (%w)", err)
	}

	return goEnv, nil
}

type ctxReaderAt struct {
	reader io.ReaderAt
	ctx    context.Context
}

var errCtxReaderCancelled = errors.New("goreinstall: context reader cancelled")

func (c *ctxReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	select {
	case _, ok := <-c.ctx.Done():
		if !ok {
			return 0, errCtxReaderCancelled
		}
	default:
	}
	return c.reader.ReadAt(p, off)
}

func getGoBinaryInfo(ctx context.Context, path string) (*buildinfo.BuildInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open file (%v) due to error (%w)", path, err)
	}
	defer func() {
		if err2 := f.Close(); err2 != nil {
			err = multierror.Append(err, err2)
		}
	}()

	if deadline, ok := ctx.Deadline(); ok {
		err = f.SetDeadline(deadline)
		if err != nil {
			return nil, fmt.Errorf("could not set file deadline (%w)", err)
		}
	}

	info, err := buildinfo.Read(&ctxReaderAt{reader: f, ctx: ctx})
	if err != nil {
		return nil, fmt.Errorf("could not Read buildinfo of (%v) due to error (%w)", path, err)
	}

	info.GoVersion = strings.ReplaceAll(info.GoVersion, "go", "")

	return info, nil
}

// getAllGoBins as a slice of paths to Go binaries in the GOBIN
func getAllGoBins(goEnv goEnvVars, verbose bool) ([]string, error) {
	if verbose {
		log.Println("running (go env)")
	}

	var binaryDir string

	if goEnv.GoBin != "" {
		binaryDir = goEnv.GoBin
	} else if goEnv.GoBin == "" && goEnv.GoPath == "" {
		return nil, errNoGoBinOrPath
	} else {
		binaryDir = filepath.Join(goEnv.GoPath, "bin")
	}

	if verbose {
		log.Printf("found go version (%v)", goEnv.GoVersion)
	}

	files, err := os.ReadDir(filepath.Clean(binaryDir))
	if err != nil {
		return nil, fmt.Errorf("could not Readdir (%v) due to error (%w)", filepath.Clean(binaryDir), err)
	}

	// prealloc since most of the time the GOBIN dir should be empty expect for Go binaries from "go install"
	paths := make([]string, 0, len(files))

	for i := range files {
		if files[i].IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(binaryDir, files[i].Name()))
	}

	return paths, nil
}

func updateBinaries(ctx context.Context, paths []string, workers int, verbose bool, goBinVer string) error {
	var eg errgroup.Group
	eg.SetLimit(workers)

	for _, path := range paths {
		path := path

		eg.Go(func() error {
			info, err := getGoBinaryInfo(ctx, path)
			if err != nil {
				return fmt.Errorf("could not getGoBinaryInfo of (%v) due to error (%w)", path, err)
			}

			if verbose {
				log.Printf("checking binary (%v) for updates", path)
			}

			ver, err := modproxy.GetLatestVersion(ctx, info.Main.Path)
			if err != nil {
				return fmt.Errorf("could not GetLatestVersion of (%v) due to error (%w)", path, err)
			}

			// if current version is not less than latest version
			if semver.Compare(info.Main.Version, ver.Version) != -1 {
				if verbose {
					log.Printf(
						"skipping updating (%v) as version (%v) is greater than or equal to latest (%v)\n",
						path,
						info.Main.Version,
						ver.Version,
					)
				}
				return nil
			}

			cmd := exec.CommandContext(ctx, "go", "install", info.Path+"@latest")
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stdout

			if err = cmd.Run(); err != nil {
				return fmt.Errorf("could not (go install %v@latest) error (%w)", info.Path, err)
			}

			return nil
		})
	}

	return eg.Wait()
}

func reinstallBinaries(ctx context.Context, paths []string, workers int, verbose bool, goBinVer string) error {
	var eg errgroup.Group
	eg.SetLimit(workers)

	for _, path := range paths {
		path := path
		eg.Go(func() error {
			info, err := getGoBinaryInfo(ctx, path)
			if err != nil {
				return fmt.Errorf("could not getGoBinaryInfo of (%v) due to error (%w)", path, err)
			}

			if semver.Compare(info.GoVersion, goBinVer) <= 0 {
				if verbose {
					log.Printf(
						"skipping (%v) as its version (%v) is equal or higher than the currently installed Go version (%v)\n",
						path,
						info.GoVersion,
						goBinVer,
					)
				}
				return nil
			}

			if verbose {
				log.Printf("reinstalling (%v)\n", path)
			}

			cmd := exec.CommandContext(ctx, "go", "install", info.Path+"@"+info.Main.Version)
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stdout

			err = cmd.Run()
			if err != nil {
				return fmt.Errorf(
					"could not (go install %v@%v) due to error (%w)",
					info.Path,
					info.GoVersion,
					err,
				)
			}

			return nil
		})
	}

	return eg.Wait()
}
