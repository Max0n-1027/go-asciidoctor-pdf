// asciidoctor-pdf は AsciiDoc 文書を PDF に変換するコマンドです。
// Asciidoctor PDF (Ruby) のコマンドラインと同じオプションを受け付けます。
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/converter"
)

const usage = `Usage: asciidoctor-pdf [OPTION]... FILE...
Convert the AsciiDoc input FILE(s) to PDF.
Example: asciidoctor-pdf input.adoc

    -d, --doctype DOCTYPE            document type to use when converting document: [article, book, manpage, inline] (default: article)
    -o, --out-file FILE              output file (default: based on path of input file); use - to output to STDOUT
        --safe                       set safe mode level to safe (default: unsafe)
    -S, --safe-mode SAFE_MODE        set safe mode level explicitly: [unsafe, safe, server, secure] (default: unsafe)
    -n, --section-numbers            auto-number section titles
    -a, --attribute name[=value]     a document attribute to set in the form of name, name!, or name=value pair
                                     this option may be specified more than once
        --theme THEME                the PDF theme to use (shorthand for -a pdf-theme=THEME)
    -B, --base-dir DIR               base directory containing the document and resources (default: directory of source file)
    -R, --source-dir DIR             source root directory (used for calculating path in destination directory)
    -D, --destination-dir DIR        destination output directory (default: directory of source file)
        --failure-level LEVEL        set minimum logging level that triggers non-zero exit code: [WARN, ERROR, FATAL] (default: FATAL)
    -q, --quiet                      silence application log messages and script warnings
        --trace                      include backtrace information when reporting errors
    -v, --verbose                    directs application messages logged at DEBUG or INFO level to STDERR
    -w, --warnings                   turn on script warnings
    -t, --timings                    print timings report
    -h, --help                       print this help message
    -V, --version                    display the version and runtime environment (or -v if no other flags or arguments)

    -b, -e, -s, -r, -I, -T, -E and --sourcemap are accepted for compatibility and ignored.
`

const toolVersion = "0.1.0"

type options struct {
	attrs       map[string]string
	doctype     string
	outFile     string
	safe        int
	baseDir     string
	sourceDir   string
	destDir     string
	quiet       bool
	verbose     bool
	timings     bool
	failLevel   int
	inputs      []string
	showHelp    bool
	showVersion bool
}

var severityRank = map[string]int{"DEBUG": 0, "INFO": 1, "WARN": 2, "ERROR": 3, "FATAL": 4}
var severityLabel = map[string]string{"DEBUG": "DEBUG", "INFO": "INFO", "WARN": "WARNING", "ERROR": "ERROR", "FATAL": "FAILED"}

type logger struct {
	quiet   bool
	verbose bool
	max     int
}

func (l *logger) Log(severity, msg string) {
	rank := severityRank[severity]
	if rank > l.max {
		l.max = rank
	}
	if l.quiet || (rank < 2 && !l.verbose) {
		return
	}
	fmt.Fprintf(os.Stderr, "asciidoctor: %s: %s\n", severityLabel[severity], msg)
}

func addAttr(attrs map[string]string, spec string) {
	if spec == "" {
		return
	}
	name, value, _ := strings.Cut(spec, "=")
	attrs[name] = value
}

func parseArgs(args []string) (*options, error) {
	o := &options{attrs: map[string]string{}, safe: asciidoc.SafeUnsafe, failLevel: severityRank["FATAL"]}
	need := func(i *int, flag string) (string, error) {
		*i++
		if *i >= len(args) {
			return "", fmt.Errorf("missing argument: %s", flag)
		}
		return args[*i], nil
	}
	safeModes := map[string]int{"unsafe": asciidoc.SafeUnsafe, "safe": asciidoc.SafeSafe, "server": asciidoc.SafeServer, "secure": asciidoc.SafeSecure}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			o.inputs = append(o.inputs, arg)
			continue
		}
		if arg == "--" {
			o.inputs = append(o.inputs, args[i+1:]...)
			break
		}
		name, inline, hasInline := arg, "", false
		if strings.HasPrefix(arg, "--") {
			if n, v, ok := strings.Cut(arg, "="); ok {
				name, inline, hasInline = n, v, true
			}
		} else if len(arg) > 2 {
			name, inline, hasInline = arg[:2], arg[2:], true
		}
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			return need(&i, name)
		}
		var v string
		var err error
		switch name {
		case "-a", "--attribute":
			if v, err = value(); err == nil {
				addAttr(o.attrs, v)
			}
		case "--theme":
			if v, err = value(); err == nil {
				o.attrs["pdf-theme"] = v
			}
		case "-d", "--doctype":
			o.doctype, err = value()
		case "-o", "--out-file":
			o.outFile, err = value()
		case "--safe":
			o.safe = asciidoc.SafeSafe
		case "-S", "--safe-mode":
			if v, err = value(); err == nil {
				mode, ok := safeModes[strings.ToLower(v)]
				if !ok {
					err = fmt.Errorf("invalid argument: %s %s", name, v)
				}
				o.safe = mode
			}
		case "-n", "--section-numbers":
			o.attrs["sectnums"] = ""
		case "-B", "--base-dir":
			o.baseDir, err = value()
		case "-R", "--source-dir":
			o.sourceDir, err = value()
		case "-D", "--destination-dir":
			o.destDir, err = value()
		case "--failure-level":
			if v, err = value(); err == nil {
				level := strings.ToUpper(v)
				if level == "WARNING" {
					level = "WARN"
				}
				rank, ok := severityRank[level]
				if !ok || rank < 2 {
					err = fmt.Errorf("invalid argument: --failure-level %s", v)
				}
				o.failLevel = rank
			}
		case "-q", "--quiet":
			o.quiet = true
		case "-v", "--verbose":
			o.verbose = true
		case "-t", "--timings":
			o.timings = true
		case "-h", "--help":
			o.showHelp = true
		case "-V", "--version":
			o.showVersion = true
		case "-w", "--warnings", "--trace", "--sourcemap", "-e", "--embedded", "-s", "--no-header-footer", "--eruby":
			// 互換性のために受け付けるが、動作には影響しない
		case "-b", "--backend", "-r", "--require", "-I", "--load-path", "-T", "--template-dir", "-E", "--template-engine":
			if v, err = value(); err == nil && (name == "-r" || name == "--require") {
				fmt.Fprintf(os.Stderr, "asciidoctor: WARNING: Ruby extension libraries are not supported; ignoring -r %s\n", v)
			}
		default:
			err = fmt.Errorf("invalid option: %s", arg)
		}
		if err != nil {
			return nil, err
		}
	}
	return o, nil
}

func printVersion() {
	fmt.Printf("Asciidoctor PDF %s (go-asciidoctor-pdf %s) using Asciidoctor %s compatible parser [https://asciidoctor.org]\n", converter.Version, toolVersion, asciidoc.Version)
	fmt.Printf("Runtime Environment (%s %s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "asciidoctor: %v\n\n%s", err, usage)
		return 1
	}
	if o.showHelp {
		fmt.Print(usage)
		return 0
	}
	if o.showVersion || (o.verbose && len(o.inputs) == 0 && len(args) == 1) {
		printVersion()
		return 0
	}
	if len(o.inputs) == 0 {
		fmt.Print(usage)
		return 0
	}
	if o.outFile != "" && o.outFile != "-" && len(o.inputs) > 1 {
		fmt.Fprintln(os.Stderr, "asciidoctor: FAILED: extra arguments detected (unparsed arguments: "+strings.Join(o.inputs[1:], ", ")+") or invalid options for multiple input files")
		return 1
	}
	log := &logger{quiet: o.quiet, verbose: o.verbose}
	for _, input := range o.inputs {
		if input != "-" {
			if st, err := os.Stat(input); err != nil || st.IsDir() {
				fmt.Fprintf(os.Stderr, "asciidoctor: FAILED: input file %s is missing\n", input)
				return 1
			}
		}
	}
	for _, input := range o.inputs {
		if err := convert(input, o, log); err != nil {
			fmt.Fprintf(os.Stderr, "asciidoctor: FAILED: %v\n", err)
			return 1
		}
	}
	if log.max >= o.failLevel {
		return 1
	}
	return 0
}

func convert(input string, o *options, log *logger) error {
	start := time.Now()
	var src []byte
	var err error
	inputFile := ""
	if input == "-" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(input)
		inputFile = input
	}
	if err != nil {
		return err
	}
	attrs := map[string]string{}
	for k, v := range o.attrs {
		attrs[k] = v
	}
	conv := converter.New(log.Log)
	doc := asciidoc.Parse(string(src), asciidoc.Options{
		Attributes: attrs,
		BaseDir:    o.baseDir,
		Safe:       o.safe,
		Doctype:    o.doctype,
		Backend:    "pdf",
		InputFile:  inputFile,
		Logger:     log,
		Inline:     conv,
	})
	parsed := time.Now()
	out := conv.Convert(doc)
	var buf bytes.Buffer
	if _, err := out.WriteTo(&buf); err != nil {
		return err
	}
	converted := time.Now()
	target := o.outFile
	switch {
	case target == "-" || (target == "" && input == "-"):
		_, err = os.Stdout.Write(buf.Bytes())
	default:
		if target == "" {
			abs, _ := filepath.Abs(input)
			name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)) + ".pdf"
			dir := filepath.Dir(abs)
			if o.destDir != "" {
				dir = o.destDir
				if o.sourceDir != "" {
					if srcRoot, e := filepath.Abs(o.sourceDir); e == nil {
						if rel, e := filepath.Rel(srcRoot, filepath.Dir(abs)); e == nil && !strings.HasPrefix(rel, "..") {
							dir = filepath.Join(o.destDir, rel)
						}
					}
				}
			}
			target = filepath.Join(dir, name)
		} else if o.destDir != "" && !filepath.IsAbs(target) {
			target = filepath.Join(o.destDir, target)
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
			err = os.WriteFile(target, buf.Bytes(), 0o644)
		}
	}
	if err != nil {
		return err
	}
	if o.timings {
		fmt.Fprintf(os.Stderr, "Input file: %s\n", input)
		fmt.Fprintf(os.Stderr, "  Time to read and parse source: %.5f\n", parsed.Sub(start).Seconds())
		fmt.Fprintf(os.Stderr, "  Time to convert document: %.5f\n", converted.Sub(parsed).Seconds())
		fmt.Fprintf(os.Stderr, "  Total time (read, parse and convert): %.5f\n", converted.Sub(start).Seconds())
	}
	return nil
}
