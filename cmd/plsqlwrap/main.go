// Command plsqlwrap wraps, unwraps, obfuscates and deobfuscates Oracle PL/SQL
// source using the V2 (Oracle 10g+ / current) format.
//
// Usage:
//
//	plsqlwrap unwrap      [-i file] [-o file]
//	plsqlwrap wrap        [-i file] [-o file] [--keep-comments] [--compat]
//	plsqlwrap obfuscate   [-i file] [-o file] [-p passphrase] [--keep-comments]
//	plsqlwrap deobfuscate [-i file] [-o file] [-p passphrase]
//
// Input is read from -i or, if omitted, stdin. Output is written to -o or, if
// omitted, stdout.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/phartenfeller/plsql-unwrap-go/plsqlwrap"
)

const version = "0.1.0 (Go port)"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "-h", "--help", "help":
		usage()
		return
	case "-V", "--version", "version":
		fmt.Println("plsqlwrap", version)
		return
	}

	args := os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var (
		inPath       = fs.String("i", "", "input file (stdin if omitted)")
		outPath      = fs.String("o", "", "output file (stdout if omitted)")
		passphrase   = fs.String("p", "", "passphrase for obfuscate/deobfuscate")
		keepComments = fs.Bool("keep-comments", false, "preserve comments (wrap/obfuscate)")
		compat       = fs.Bool("compat", false, "strip schema. prefix before wrapping")
	)
	// Long aliases.
	fs.StringVar(inPath, "input", "", "input file (stdin if omitted)")
	fs.StringVar(outPath, "output", "", "output file (stdout if omitted)")
	fs.StringVar(passphrase, "passphrase", "", "passphrase for obfuscate/deobfuscate")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	input, err := readInput(*inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	var result string
	switch cmd {
	case "unwrap":
		result = plsqlwrap.UnwrapAll(input)
	case "wrap":
		src := input
		if *compat {
			src = plsqlwrap.ApplyCompat(src)
		}
		result = plsqlwrap.WrapV2(src, *keepComments, false)
	case "obfuscate", "obf":
		if *passphrase == "" {
			fmt.Fprintln(os.Stderr, "Error: -p passphrase is required for obfuscate")
			os.Exit(1)
		}
		result, err = obfuscateAndWrap(input, *passphrase, *keepComments)
	case "deobfuscate", "deobf":
		if *passphrase == "" {
			fmt.Fprintln(os.Stderr, "Error: -p passphrase is required for deobfuscate")
			os.Exit(1)
		}
		result, err = unwrapAndDeobfuscate(input, *passphrase)
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	if err := writeOutput(*outPath, result); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if result == "" {
		os.Exit(1)
	}
}

// obfuscateAndWrap obfuscates the source, then wraps each CREATE unit before
// the trailing --ENC: block (mirroring the reference CLI orchestration).
func obfuscateAndWrap(input, passphrase string, keepComments bool) (string, error) {
	obf, err := plsqlwrap.Obfuscate(input, passphrase, keepComments)
	if err != nil {
		return "", err
	}
	return plsqlwrap.WrapObfuscated(obf, keepComments), nil
}

func unwrapAndDeobfuscate(input, passphrase string) (string, error) {
	parts := plsqlwrap.SplitEnc(input)
	unwrapped := plsqlwrap.UnwrapAll(parts.Body)
	return plsqlwrap.Deobfuscate(unwrapped+"\n"+parts.Enc, passphrase)
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
	fmt.Fprint(os.Stderr, `plsqlwrap `+version+` — wrap/unwrap Oracle PL/SQL (V2 format)

Usage:
  plsqlwrap unwrap      [-i file] [-o file]
  plsqlwrap wrap        [-i file] [-o file] [--keep-comments] [--compat]
  plsqlwrap obfuscate   [-i file] [-o file] -p passphrase [--keep-comments]
  plsqlwrap deobfuscate [-i file] [-o file] -p passphrase

Reads stdin when -i is omitted; writes stdout when -o is omitted.
`)
}
