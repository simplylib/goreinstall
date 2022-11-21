package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/simplylib/errgroup"
	"github.com/simplylib/multierror"
	"golang.org/x/mod/semver"
)

type GoEnv struct {
	GoBin     string `json:"GOBIN"`
	GoPath    string `json:"GOPATH"`
	GoVersion string `json:"GOVERSION"`
}

func getGoEnv(ctx context.Context) (GoEnv, error) {
	cmd := exec.CommandContext(ctx, "go", "env", "-json")
	cmd.Stderr = os.Stderr

	stdoutBuf := &bytes.Buffer{}
	cmd.Stdout = stdoutBuf

	err := cmd.Run()
	if err != nil {
		return GoEnv{}, fmt.Errorf("could not run (go env -json) due to error (%w)", err)
	}

	goEnv := GoEnv{}
	err = json.Unmarshal(stdoutBuf.Bytes(), &goEnv)
	if err != nil {
		return GoEnv{}, fmt.Errorf("could not parse JSON from (go env -json) due to error (%w)", err)
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

// getAllGoBins returns a slice of paths to Go binaries in the GOBIN, a string which is the semver of the detected Go compiler's version, and the error
func getAllGoBins(ctx context.Context, verbose bool) ([]string, string, error) {
	if verbose {
		log.Println("running (go env)")
	}

	goEnv, err := getGoEnv(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("could not get goEnv (%w)", err)
	}

	var binaryDir string

	if goEnv.GoBin != "" {
		binaryDir = goEnv.GoBin
	} else if goEnv.GoBin == "" && goEnv.GoPath == "" {
		return nil, "", errNoGoBinOrPath
	} else {
		binaryDir = filepath.Join(goEnv.GoPath, "bin")
	}

	if verbose {
		log.Printf("found go version (%v)", goEnv.GoVersion)
	}

	files, err := os.ReadDir(filepath.Clean(binaryDir))
	if err != nil {
		return nil, "", fmt.Errorf("could not Readdir (%v) due to error (%w)", filepath.Clean(binaryDir), err)
	}

	// prealloc since most of the time the GOBIN dir should be empty expect for Go binaries from "go install"
	paths := make([]string, 0, len(files))

	for i := range files {
		if files[i].IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(binaryDir, files[i].Name()))
	}

	return paths, strings.ReplaceAll(goEnv.GoVersion, "go", ""), nil
}

func reinstallBinaries(ctx context.Context, paths []string, workers int, update bool, verbose bool, goBinVer string) error {
	var eg errgroup.Group
	eg.SetLimit(workers)

	for _, path := range paths {
		path := path
		eg.Go(func() error {
			info, err := getGoBinaryInfo(ctx, path)
			if err != nil {
				return fmt.Errorf("could not getGoBinaryInfo of (%v) due to error (%w)", path, err)
			}

			if update {
				if verbose {
					log.Printf("updating binary (%v)", path)
				}

				cmd := exec.CommandContext(ctx, "go", "install", info.Path+"@latest")
				cmd.Stderr = os.Stderr
				cmd.Stdout = os.Stdout

				err = cmd.Run()
				if err != nil {
					return fmt.Errorf(
						"could not (go install %v@latest) due to error (%w)",
						info.Path,
						err,
					)
				}

				return nil
			}

			if semver.Compare(info.GoVersion, goBinVer) == -1 {
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

	return nil
}

var errNoGoBinOrPath = errors.New("goreinstall: unable to find a GOPATH or GOBIN from command (go env -json)")

func run() error {
	verbose := flag.Bool("v", false, "be verbose about operations")
	all := flag.Bool("a", false, "reinstall all binaries in GOBIN (eX: after go version update)")
	maxWorkers := flag.Int("t", runtime.NumCPU()*2, "max number of binaries to reinstall at once")
	update := flag.Bool("u", false, "update binaries if there is an update available")

	flag.CommandLine.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), os.Args[0]+" reinstalls modules with new versions or when the go version is lower than the current one")
		fmt.Fprintln(flag.CommandLine.Output(), "\nUsage: "+os.Args[0]+" [flags] <package(s) ...>")
		fmt.Fprintln(flag.CommandLine.Output(), "Ex: "+os.Args[0]+" -a             // reinstall all binaries in GOBIN")
		fmt.Fprintln(flag.CommandLine.Output(), "Ex: "+os.Args[0]+" -a -u          // reinstall all binaries and update if needed")
		fmt.Fprintln(flag.CommandLine.Output(), "Ex: "+os.Args[0]+" goreinstall -u // reinstall goreinstall and update if needed")
		fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
		flag.CommandLine.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() == 0 && !*all {
		log.Println("Expected at least 1 package", "\n")
		flag.CommandLine.Usage()
		return nil
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	go func() {
		osSignal := make(chan os.Signal, 1)
		signal.Notify(osSignal, syscall.SIGTERM, os.Interrupt)

		s := <-osSignal
		log.Printf("Cancelling operations due to (%v)\n", s.String())
		cancelFunc()
		log.Println("operations cancelled")
	}()

	var (
		goBinVer string
		paths    []string
	)
	if *all {
		var err error
		paths, goBinVer, err = getAllGoBins(ctx, *verbose)
		if err != nil {
			return fmt.Errorf("could not getAllGoBins (%w)", err)
		}
	} else {
		paths = flag.Args()
	}

	if *verbose {
		log.Println("going to try and check if we need to reinstall these binaries")

		for i := range paths {
			log.Println("\t" + paths[i])
		}
	}

	return reinstallBinaries(ctx, paths, *maxWorkers, *update, *verbose, goBinVer)
}

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}
