// Flag and environment helpers shared by the commands, and nonNilList for
// their JSON list output.

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/krimvp/comitia"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newFlags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}

// parseFlags rejects leftovers: flag parsing stops at the first positional
// token, so an unquoted value would otherwise be truncated silently.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := parseQuiet(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return comitia.Invalidf("unexpected argument %q; quote values that contain spaces", fs.Arg(0))
	}
	return nil
}

// parseQuiet prints usage only for --help, so a parse error leaves stderr with
// just the JSON error line.
func parseQuiet(fs *flag.FlagSet, args []string) error {
	out := fs.Output()
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	err := fs.Parse(args)
	fs.SetOutput(out)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = out.Write(buf.Bytes())
		return err
	}
	if err != nil {
		help := "comitia " + fs.Name() + " --help"
		if fs.Name() == "comitia" {
			help = "comitia --help"
		}
		return comitia.Invalidf("%v; see %s", err, help)
	}
	return nil
}

// requireFlags names the first missing required flag. Commands call it before
// their parse-only return, so the error precedes storage and network access.
func requireFlags(fs *flag.FlagSet, names ...string) error {
	for _, name := range names {
		if err := requireAny(fs, name); err != nil {
			return err
		}
	}
	return nil
}

// requireAny accepts a nonempty value for one of names; unset numeric flags
// count as missing even though their zero default prints as a value.
func requireAny(fs *flag.FlagSet, names ...string) error {
	for _, name := range names {
		f := fs.Lookup(name)
		if v := f.Value.String(); v != "" && (v != f.DefValue || flagProvided(fs, name)) {
			return nil
		}
	}
	return comitia.Invalidf("%s requires --%s", fs.Name(), strings.Join(names, " or --"))
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

func booleanEnv(fs *flag.FlagSet, name, key string, value *bool) error {
	if flagProvided(fs, name) || os.Getenv(key) == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fmt.Errorf("%w: %s must be a boolean", comitia.ErrInvalid, key)
	}
	*value = parsed
	return nil
}

func nonNilList[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
