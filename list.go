package main

import (
	"context"
	"debug/buildinfo"
	"flag"
	"fmt"
	"log"
	"os"
)

// listCommand runs "goreinstall -l".
func listCommand(ctx context.Context, args []string) error {
	flagSet := flag.FlagSet{}
	flagSet.Usage = func() {
		fmt.Fprintln(flagSet.Output(),
			os.Args[0]+os.Args[1]+": lists modules in GoBin with their versions and Go compiler versions",
		)
		flagSet.PrintDefaults()
	}

	verbose := flagSet.Bool("v", false, "be verbose about operations")

	err := flagSet.Parse(args)
	if err != nil {
		return fmt.Errorf("could not parse args for list command error (%w)", err)
	}

	if *verbose {
		log.Println("getting GoEnv from commandline (go env -json)")
	}

	goEnv, err := getGoEnv(ctx, "go")
	if err != nil {
		return fmt.Errorf("could not getGoEnv (%w)", err)
	}

	if *verbose {
		log.Printf("got these values from GoEnv (%#+v)\n", goEnv)
	}

	paths, err := getAllGoBins(goEnv, *verbose)
	if err != nil {
		return fmt.Errorf("could not getAllGoBins (%w)", err)
	}

	var info *buildinfo.BuildInfo
	for i := range paths {
		select {
		case <-ctx.Done():
			break
		default:
		}

		info, err = getGoBinaryInfo(ctx, paths[i])
		if err != nil {
			return fmt.Errorf("could not getGoBinaryInfo for (%v) error (%w)", paths[i], err)
		}
		log.Printf("%v %s@%s\n", info.GoVersion, info.Main.Path, info.Main.Version)
	}

	return nil
}
