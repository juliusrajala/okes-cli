package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
	"go-sesh/internal/workspace"
)

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usage(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

type command struct {
	name     string
	stateDir string
	json     bool
	help     bool
	selector string
	id       session.ID
	start    session.StartRequest
}

type startOptions struct {
	detached    bool
	network     string
	mode        string
	environment environmentFlags
}

func flagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	return flags
}

func commonFlags(flags *flag.FlagSet, cmd *command) {
	flags.StringVar(&cmd.stateDir, "state-dir", cmd.stateDir, "Private state directory")
	flags.BoolVar(&cmd.json, "json", cmd.json, "Write machine-readable JSON")
}

func parse(args []string) (command, error) {
	var cmd command
	root := flagSet("sesh")
	commonFlags(root, &cmd)
	if err := root.Parse(args); err != nil {
		if err == flag.ErrHelp {
			cmd.help = true
			return cmd, nil
		}
		return cmd, usage("%v", err)
	}
	if root.NArg() == 0 {
		cmd.help = true
		return cmd, nil
	}
	cmd.name = root.Arg(0)
	switch cmd.name {
	case "start", "inspect", "list", "stop", "remove":
	default:
		return cmd, usage("unknown command %q", cmd.name)
	}
	args = root.Args()[1:]
	var workload []string
	if cmd.name == "start" {
		if index := slices.Index(args, "--"); index >= 0 {
			workload = slices.Clone(args[index+1:])
			args = args[:index]
		}
	}
	flags := flagSet(cmd.name)
	commonFlags(flags, &cmd)
	var start startOptions
	var id string
	if cmd.name == "start" {
		flags.StringVar(&cmd.start.Name, "name", "", "Unique session name (required)")
		flags.StringVar(&cmd.start.Image, "image", "", "Existing local image (required)")
		flags.BoolVar(&start.detached, "detach", false, "Return after starting (required)")
		flags.StringVar(&start.network, "network", "none", "none or pasta")
		flags.StringVar(&start.mode, "workspace", "mount", "Only mount is implemented")
		flags.Var(&start.environment, "env", "Literal container NAME=VALUE; repeatable")
	} else if cmd.name != "list" {
		flags.StringVar(&id, "id", "", "Immutable UUID instead of a session name")
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			cmd.help = true
			return cmd, nil
		}
		return cmd, usage("%v", err)
	}
	if strings.ContainsRune(cmd.stateDir, '\x00') {
		return cmd, usage("state directory must contain no NUL bytes")
	}
	switch cmd.name {
	case "start":
		return parseStart(cmd, start, flags.Args(), workload)
	case "list":
		if flags.NArg() != 0 {
			return cmd, usage("list accepts no positional arguments")
		}
		return cmd, nil
	default:
		idProvided := false
		flags.Visit(func(value *flag.Flag) { idProvided = idProvided || value.Name == "id" })
		return parseSelector(cmd, id, idProvided, flags.Args())
	}
}

func parseStart(cmd command, options startOptions, operands, workload []string) (command, error) {
	if !options.detached {
		return cmd, usage("only detached start is implemented; add --detach")
	}
	if err := session.ValidateName(cmd.start.Name); err != nil {
		return cmd, usage("--name: %v", err)
	}
	if cmd.start.Image == "" || strings.ContainsRune(cmd.start.Image, '\x00') {
		return cmd, usage("--image must select an existing local image")
	}
	if err := workspace.Mode(options.mode).Validate(); err != nil {
		return cmd, usage("--workspace: %v", err)
	}
	if options.mode != "mount" {
		return cmd, usage("copy workspace preparation is not implemented")
	}
	cmd.start.Network = runtime.NetworkMode(options.network)
	if err := cmd.start.Network.Validate(); err != nil {
		return cmd, usage("--network: %v", err)
	}
	if len(operands) != 1 || operands[0] == "" || strings.ContainsRune(operands[0], '\x00') {
		return cmd, usage("start requires one workspace directory; put flags before the directory")
	}
	if len(workload) == 0 {
		return cmd, usage("pass a nonempty workload argv after --")
	}
	for _, arg := range workload {
		if arg == "" || strings.ContainsRune(arg, '\x00') {
			return cmd, usage("workload arguments must be nonempty and contain no NUL bytes")
		}
	}
	values, err := parseEnvironment(options.environment)
	if err != nil {
		return cmd, usage("--env: %v", err)
	}
	cmd.start.Environment = values
	cmd.start.Command = workload
	cmd.start.WorkspacePath = operands[0]
	return cmd, nil
}

func parseSelector(cmd command, id string, idProvided bool, operands []string) (command, error) {
	if idProvided {
		if len(operands) != 0 {
			return cmd, usage("use either a name or --id UUID, not both")
		}
		parsed, err := session.ParseID(id)
		if err != nil {
			return cmd, usage("--id: %v", err)
		}
		cmd.id = parsed
	} else {
		if len(operands) != 1 {
			return cmd, usage("%s requires a session name or --id UUID", cmd.name)
		}
		if err := session.ValidateName(operands[0]); err != nil {
			return cmd, usage("%v", err)
		}
		cmd.selector = operands[0]
	}
	return cmd, nil
}

type environmentFlags []string

// Keep environment values out of flag defaults and help output.
func (values *environmentFlags) String() string { return "" }

func (values *environmentFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseEnvironment(values []string) (map[string]string, error) {
	environment := make(map[string]string, len(values))
	for _, entry := range values {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("use NAME=VALUE syntax")
		}
		if _, exists := environment[name]; exists {
			return nil, fmt.Errorf("variable %q is repeated", name)
		}
		environment[name] = value
	}
	return environment, nil
}

// defaultStateDir intentionally avoids the TypeScript pi-sesh state namespace.
// An empty XDG_DATA_HOME falls back to HOME, as in the reference.
func defaultStateDir(environment []string) (string, error) {
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	base := values["XDG_DATA_HOME"]
	if base == "" {
		home := values["HOME"]
		if !filepath.IsAbs(home) || strings.ContainsRune(home, '\x00') {
			return "", fmt.Errorf("absolute HOME or XDG_DATA_HOME is required for session state")
		}
		base = filepath.Join(home, ".local", "share")
	}
	if !filepath.IsAbs(base) || strings.ContainsRune(base, '\x00') {
		return "", fmt.Errorf("XDG_DATA_HOME must be absolute and contain no NUL bytes")
	}
	return filepath.Join(base, "go-sesh"), nil
}
