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
	"syscall"

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

	if deadline, ok := ctx.Deadline(); ok {
		f.SetDeadline(deadline)
	}

	info, err := buildinfo.Read(&ctxReaderAt{reader: f, ctx: ctx})
	if err != nil {
		return nil, fmt.Errorf("could not Read buildinfo of (%v) due to error (%w)", path, err)
	}

	return info, nil
}

var errNoGoBinOrPath = errors.New("goreinstall: unable to find a GOPATH or GOBIN from command (go env -json)")

func run() error {
	verbose := flag.Bool("v", false, "be verbose about operations")
	all := flag.Bool("a", false, "reinstall all binaries in GOBIN (eX: after go version update)")
	//update := flag.Bool("u", false, "update binaries if there is an update available")

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
		log.Println("Expected at least 1 package")
		return nil
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	go func() {
		osSignal := make(chan os.Signal, 1)
		signal.Notify(osSignal, syscall.SIGTERM, os.Interrupt)

		select {
		case s := <-osSignal:
			log.Printf("Cancelling operations due to (%v)\n", s.String())
			cancelFunc()
			log.Println("operations cancelled")
		}
	}()

	var goBinVer string
	var binaryPaths []string
	if *all {
		if *verbose {
			log.Println("running (go env)")
		}

		goEnv, err := getGoEnv(ctx)
		if err != nil {
			return fmt.Errorf("could not get goEnv (%w)", err)
		}

		var binaryDir string

		if goEnv.GoBin != "" {
			binaryDir = goEnv.GoBin
		} else if goEnv.GoBin == "" && goEnv.GoPath == "" {
			return errNoGoBinOrPath
		} else {
			binaryDir = filepath.Join(goEnv.GoPath, "bin")
		}

		goBinVer = goEnv.GoVersion
		if *verbose {
			log.Printf("found go version (%v)", goBinVer)
		}

		files, err := os.ReadDir(filepath.Clean(binaryDir))
		if err != nil {
			return fmt.Errorf("could not Readdir (%v) due to error (%w)", filepath.Clean(binaryDir), err)
		}

		for i := range files {
			if files[i].IsDir() {
				continue
			}
			binaryPaths = append(binaryPaths, filepath.Join(binaryDir, files[i].Name()))
		}
	} else {
		binaryPaths = flag.Args()
	}

	if *verbose {
		log.Println("going to try and check if we need to reinstall these binaries")

		for i := range binaryPaths {
			log.Println("\t" + binaryPaths[i])
		}
	}

	var (
		info *buildinfo.BuildInfo
		err  error
	)
	for _, path := range binaryPaths {
		info, err = getGoBinaryInfo(ctx, path)
		if err != nil {
			return fmt.Errorf("could not getGoBinaryInfo of (%v) due to error (%w)", path, err)
		}

		if semver.Compare(info.GoVersion, goBinVer) > -1 {
			if *verbose {
				log.Printf(
					"skipping (%v) as its version (%v) is equal or higher than the currently installed Go version (%v)\n",
					path,
					info.GoVersion,
					goBinVer,
				)
			}
			continue
		}

		if *verbose {
			log.Printf("reinstalling (%v)\n", path)
		}

		cmd := exec.CommandContext(ctx, "go", "install", info.Path+"@"+info.GoVersion)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout

		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("could not (go install %v@%v) due to error (%w)", info.Path, info.GoVersion, err)
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}
