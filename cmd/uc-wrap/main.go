// Command uc-wrap wraps, obfuscates and deobfuscates Oracle PL/SQL
// source using the V2 (Oracle 10g+ / current) format.
//
// Usage:
//
//	uc-wrap [wrap]      [-i file|glob...] [-o file] [--keep-comments] [--compat]
//	uc-wrap obfuscate   [-i file|glob...] [-o file] -p passphrase [--keep-comments]
//	uc-wrap deobfuscate [-i file] [-o file] -p passphrase
//
// Input is read from -i or, if omitted, stdin. Output is written to -o or, if
// omitted, stdout. Exception: wrap/obfuscate of "-i x.pkb" without -o writes
// "x.pkw" next to the input; use "-o -" to force stdout. wrap and obfuscate
// accept several inputs (a glob such as -i ./*.pkb, expanded by the shell or by
// uc-wrap itself) and write each result next to its .pkb input. Without a
// subcommand, wrap is assumed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/United-Codes/uc-wrap/ucwrap"
)

// version is overridden at release time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "-h", "--help", "help":
		usage()
		return
	case "-V", "--version", "version":
		fmt.Println("uc-wrap", version)
		return
	}
	if strings.HasPrefix(cmd, "-") {
		// No subcommand given (e.g. `uc-wrap -i *.pkb`): default to wrap.
		cmd, args = "wrap", os.Args[1:]
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var (
		inPath       = fs.String("i", "", "input file or glob (stdin if omitted)")
		outPath      = fs.String("o", "", "output file (stdout if omitted)")
		passphrase   = fs.String("p", "", "passphrase for obfuscate/deobfuscate")
		keepComments = fs.Bool("keep-comments", false, "preserve comments (wrap/obfuscate)")
		compat       = fs.Bool("compat", false, "strip schema. prefix before wrapping")
	)
	// Long aliases.
	fs.StringVar(inPath, "input", "", "input file or glob (stdin if omitted)")
	fs.StringVar(outPath, "output", "", "output file (stdout if omitted)")
	fs.StringVar(passphrase, "passphrase", "", "passphrase for obfuscate/deobfuscate")

	// A shell expands `-i ./*.pkb` into `-i a.pkb b.pkb c.pkb`, so extra
	// positional arguments are further input files. Keep parsing after them so
	// flags may follow the file list.
	var extra []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		extra = append(extra, rest[0])
		rest = rest[1:]
	}

	switch cmd {
	case "wrap", "obfuscate", "obf", "deobfuscate", "deobf":
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", cmd)
		usage()
		os.Exit(2)
	}

	opts := options{cmd: cmd, passphrase: *passphrase, keepComments: *keepComments, compat: *compat}

	var requested []string
	if *inPath != "" {
		requested = append(requested, *inPath)
	}
	requested = append(requested, extra...)
	inputs, err := expandInputs(requested)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	if len(inputs) > 1 {
		os.Exit(runBatch(opts, inputs, *outPath))
	}

	// Single input (or stdin).
	in := ""
	if len(inputs) == 1 {
		in = inputs[0]
	}
	input, err := readInput(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	// wrap/obfuscate: -i foo.pkb with no -o writes foo.pkw next to the input.
	out := *outPath
	if out == "" && opts.writesWrapped() {
		out = defaultWrappedPath(in)
	}
	if out == "-" {
		out = ""
	}

	result, err := opts.process(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if err := writeOutput(out, result); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if result == "" {
		os.Exit(1)
	}
}

type options struct {
	cmd          string
	passphrase   string
	keepComments bool
	compat       bool
}

// writesWrapped reports whether the command's output is a wrapped unit that
// defaults to a .pkw file next to a .pkb input.
func (o options) writesWrapped() bool {
	return o.cmd == "wrap" || o.cmd == "obfuscate" || o.cmd == "obf"
}

// process runs the command on one input's text.
func (o options) process(input string) (string, error) {
	switch o.cmd {
	case "wrap":
		src := input
		if o.compat {
			src = ucwrap.ApplyCompat(src)
		}
		return ucwrap.WrapV2(src, o.keepComments, false), nil
	case "obfuscate", "obf":
		if o.passphrase == "" {
			return "", errors.New("-p passphrase is required for obfuscate")
		}
		return obfuscateAndWrap(input, o.passphrase, o.keepComments)
	default: // deobfuscate
		if o.passphrase == "" {
			return "", errors.New("-p passphrase is required for deobfuscate")
		}
		return unwrapAndDeobfuscate(input, o.passphrase)
	}
}

// expandInputs expands glob patterns (for shells that don't, e.g. Windows
// cmd.exe, or when the pattern is quoted: -i "src/*.pkb"). An argument naming an
// existing file is used as is; a pattern matching nothing is an error.
func expandInputs(args []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, a := range args {
		if _, err := os.Stat(a); err != nil && strings.ContainsAny(a, "*?[") {
			matches, gerr := filepath.Glob(a)
			if gerr != nil {
				return nil, fmt.Errorf("bad pattern %q: %w", a, gerr)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no files match %q", a)
			}
			sort.Strings(matches)
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					out = append(out, m)
				}
			}
			continue
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// runBatch processes several input files, writing each result next to its
// input as name.pkw. Only wrap/obfuscate support this, and every input must be
// a .pkb file. It keeps going after a failure and returns the exit code.
func runBatch(opts options, inputs []string, outPath string) int {
	if !opts.writesWrapped() {
		fmt.Fprintf(os.Stderr, "Error: %s takes a single input file\n", opts.cmd)
		return 2
	}
	if outPath != "" {
		fmt.Fprintln(os.Stderr, "Error: -o cannot be combined with multiple input files (each is written to <name>.pkw)")
		return 2
	}
	failed := 0
	for _, in := range inputs {
		out := defaultWrappedPath(in)
		if out == "" {
			fmt.Fprintf(os.Stderr, "Error: %s: multiple inputs must be .pkb files\n", in)
			failed++
			continue
		}
		input, err := readInput(in)
		if err == nil {
			var result string
			if result, err = opts.process(input); err == nil && result == "" {
				err = errors.New("nothing to wrap")
			}
			if err == nil {
				err = writeOutput(out, result)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s: %v\n", in, err)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "%s -> %s\n", in, out)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d of %d files failed\n", failed, len(inputs))
		return 1
	}
	return 0
}

// obfuscateAndWrap obfuscates the source, then wraps each CREATE unit before
// the trailing --ENC: block (mirroring the reference CLI orchestration).
func obfuscateAndWrap(input, passphrase string, keepComments bool) (string, error) {
	obf, err := ucwrap.Obfuscate(input, passphrase, keepComments)
	if err != nil {
		return "", err
	}
	return ucwrap.WrapObfuscated(obf, keepComments), nil
}

func unwrapAndDeobfuscate(input, passphrase string) (string, error) {
	parts := ucwrap.SplitEnc(input)
	unwrapped := ucwrap.UnwrapAll(parts.Body)
	return ucwrap.Deobfuscate(unwrapped+"\n"+parts.Enc, passphrase)
}

// defaultWrappedPath maps "dir/name.pkb" to "dir/name.pkw". It returns "" (stdout)
// when there is no input file or the extension is not .pkb.
func defaultWrappedPath(in string) string {
	if strings.EqualFold(filepath.Ext(in), ".pkb") {
		return strings.TrimSuffix(in, filepath.Ext(in)) + ".pkw"
	}
	return ""
}

func readInput(path string) (string, error) {
	if path != "" {
		b, err := os.ReadFile(path)
		return string(b), err
	}
	b, err := io.ReadAll(os.Stdin)
	return string(b), err
}

func writeOutput(path, content string) error {
	if path != "" {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	if content != "" {
		_, err := io.WriteString(os.Stdout, content)
		return err
	}
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `uc-wrap `+version+` — wrap Oracle PL/SQL (V2 format)

Usage:
  uc-wrap [wrap]      [-i file|glob...] [-o file] [--keep-comments] [--compat]
  uc-wrap obfuscate   [-i file|glob...] [-o file] -p passphrase [--keep-comments]
  uc-wrap deobfuscate [-i file] [-o file] -p passphrase

Reads stdin when -i is omitted; writes stdout when -o is omitted.
wrap/obfuscate with -i x.pkb and no -o write x.pkw next to the input
(use -o - for stdout). Several inputs (a glob like -i ./*.pkb) are each
written to their own .pkw; -o cannot be combined with them.
Without a subcommand, wrap is assumed.
`)
}
