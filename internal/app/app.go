package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oustn/cloudflare-dns-dnspod/internal/cloudflare"
	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/dnscheck"
	"github.com/oustn/cloudflare-dns-dnspod/internal/dnspod"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
	"github.com/oustn/cloudflare-dns-dnspod/internal/workflow"
)

const usage = `cf-dnspod configures DNSPod child zones for Cloudflare for SaaS.

Usage:
  cf-dnspod add HOSTNAME [--zone ZONE] [--origin[=TARGET]] [--wait] [--dry-run]
  cf-dnspod set-edge HOSTNAME (--host HOST | --ip IPV4) [--zone ZONE] [--dry-run]
  cf-dnspod set-backend HOSTNAME TARGET [--zone ZONE] [--dry-run]
  cf-dnspod status HOSTNAME [--zone ZONE]

Commands:
  add       onboard a new subdomain or resume interrupted onboarding
  set-edge  update only the DNSPod apex A/CNAME after readiness checks
  set-backend update only the Cloudflare origin backend after readiness checks
  status    inspect current state without provider writes
`

type Factory func(config.Config) (workflow.Services, error)

type Runner struct {
	EnvPath string
	Factory Factory
}

func DefaultRunner() Runner {
	return Runner{EnvPath: ".env", Factory: defaultFactory}
}

func defaultFactory(cfg config.Config) (workflow.Services, error) {
	dns, err := dnspod.New(cfg.DNSPodSecretID, cfg.DNSPodSecretKey, cfg.DNSPodRecordLine)
	if err != nil {
		return workflow.Services{}, err
	}
	cf := cloudflare.New(cfg.CFAPIToken, "https://api.cloudflare.com/client/v4", &http.Client{Timeout: 20 * time.Second})
	return workflow.Services{Cloudflare: cf, DNSPod: dns, Resolver: dnscheck.NewPublic()}, nil
}

type parsed struct {
	command               config.Command
	hostname, zone        string
	origin, backend       string
	originSet             bool
	wait, dryRun, replace bool
	host, ip              string
	timeout, poll         time.Duration
}

func (r Runner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	options, err := parse(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Input error: %v\n", err)
		return 2
	}
	if err := validateParsed(options); err != nil {
		fmt.Fprintf(stderr, "Input error: %v\n", err)
		return 2
	}
	envPath := r.EnvPath
	if envPath == "" {
		envPath = ".env"
	}
	cfg, err := config.Load(envPath, options.command)
	if err != nil {
		fmt.Fprintf(stderr, "Configuration error: %v\n", err)
		return 2
	}
	factory := r.Factory
	if factory == nil {
		factory = defaultFactory
	}
	services, err := factory(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", cfg.Redact(err.Error()))
		return 1
	}
	var result domain.OperationResult
	switch options.command {
	case config.CommandAdd:
		result, err = workflow.Add(ctx, cfg, options.hostname, services, workflow.AddOptions{
			Zone: options.zone, OriginSet: options.originSet, Origin: options.origin,
			Wait: options.wait, DryRun: options.dryRun, ReplaceStaleNS: options.replace,
			Timeout: options.timeout, PollInterval: options.poll,
		})
	case config.CommandSetEdge:
		result, err = workflow.SetEdge(ctx, cfg, options.hostname, workflow.EdgeTarget{Host: options.host, IP: options.ip}, services, workflow.EdgeOptions{Zone: options.zone, DryRun: options.dryRun})
	case config.CommandSetBackend:
		result, err = workflow.SetBackend(ctx, cfg, options.hostname, options.backend, services, workflow.BackendOptions{Zone: options.zone, DryRun: options.dryRun})
	case config.CommandStatus:
		result, err = workflow.Status(ctx, cfg, options.hostname, services)
	default:
		err = errors.New("unsupported command")
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", cfg.Redact(err.Error()))
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}
	return 0
}

func parse(args []string, stderr io.Writer) (parsed, error) {
	result := parsed{timeout: 300 * time.Second, poll: 5 * time.Second}
	switch args[0] {
	case "add":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return result, fmt.Errorf("add requires a positional HOSTNAME")
		}
		result.command = config.CommandAdd
		result.hostname = args[1]
		set := flag.NewFlagSet("add", flag.ContinueOnError)
		set.SetOutput(stderr)
		set.StringVar(&result.zone, "zone", "", "Cloudflare for SaaS Zone name")
		origin := &originFlag{}
		set.Var(origin, "origin", "use a same-name Custom Origin, optionally with =TARGET")
		set.BoolVar(&result.wait, "wait", false, "wait for DNS propagation and activation")
		set.BoolVar(&result.dryRun, "dry-run", false, "show planned changes")
		set.BoolVar(&result.replace, "replace-stale-ns", false, "replace only stale NS records")
		timeout := set.Float64("timeout", 300, "maximum wait in seconds")
		poll := set.Float64("poll-interval", 5, "poll interval in seconds")
		if err := set.Parse(args[2:]); err != nil {
			return result, err
		}
		if set.NArg() != 0 {
			return result, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
		}
		result.timeout = time.Duration(*timeout * float64(time.Second))
		result.poll = time.Duration(*poll * float64(time.Second))
		result.originSet, result.origin = origin.set, origin.value
	case "set-edge":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return result, fmt.Errorf("set-edge requires a positional HOSTNAME")
		}
		result.command = config.CommandSetEdge
		result.hostname = args[1]
		set := flag.NewFlagSet("set-edge", flag.ContinueOnError)
		set.SetOutput(stderr)
		set.StringVar(&result.zone, "zone", "", "Cloudflare for SaaS Zone name")
		set.StringVar(&result.host, "host", "", "preferred CNAME target")
		set.StringVar(&result.ip, "ip", "", "preferred globally-routable IPv4")
		set.BoolVar(&result.dryRun, "dry-run", false, "show planned change")
		if err := set.Parse(args[2:]); err != nil {
			return result, err
		}
		if set.NArg() != 0 {
			return result, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
		}
	case "set-backend":
		if len(args) < 3 || strings.HasPrefix(args[1], "-") || strings.HasPrefix(args[2], "-") {
			return result, fmt.Errorf("set-backend requires positional HOSTNAME and TARGET")
		}
		result.command = config.CommandSetBackend
		result.hostname, result.backend = args[1], args[2]
		set := flag.NewFlagSet("set-backend", flag.ContinueOnError)
		set.SetOutput(stderr)
		set.StringVar(&result.zone, "zone", "", "Cloudflare for SaaS Zone name")
		set.BoolVar(&result.dryRun, "dry-run", false, "show planned change")
		if err := set.Parse(args[3:]); err != nil {
			return result, err
		}
		if set.NArg() != 0 {
			return result, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
		}
	case "status":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return result, fmt.Errorf("status requires a positional HOSTNAME")
		}
		result.command = config.CommandStatus
		result.hostname = args[1]
		set := flag.NewFlagSet("status", flag.ContinueOnError)
		set.SetOutput(stderr)
		set.StringVar(&result.zone, "zone", "", "Cloudflare for SaaS Zone name")
		if err := set.Parse(args[2:]); err != nil {
			return result, err
		}
		if set.NArg() != 0 {
			return result, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
		}
	default:
		return result, fmt.Errorf("unknown command %q", args[0])
	}
	if _, err := domain.NormalizeHostname(result.hostname); err != nil {
		return result, err
	}
	return result, nil
}

func validateParsed(options parsed) error {
	if options.zone != "" {
		if _, err := domain.NormalizeHostname(options.zone); err != nil {
			return fmt.Errorf("--zone: %w", err)
		}
	}
	if options.command == config.CommandAdd {
		if options.timeout < 0 || options.poll < 0 {
			return fmt.Errorf("--timeout and --poll-interval must be non-negative")
		}
		if options.originSet && options.origin != "" {
			_, _, err := domain.ClassifyTarget(options.origin, "origin")
			return err
		}
		return nil
	}
	if options.command == config.CommandSetEdge {
		hostSet := strings.TrimSpace(options.host) != ""
		ipSet := strings.TrimSpace(options.ip) != ""
		if hostSet == ipSet {
			return fmt.Errorf("exactly one of --host and --ip is required")
		}
		if hostSet {
			_, err := domain.NormalizeHostname(options.host)
			return err
		}
		return domain.ValidatePublicIPv4(options.ip)
	}
	if options.command == config.CommandSetBackend {
		_, _, err := domain.ClassifyTarget(options.backend, "backend")
		return err
	}
	return nil
}

type originFlag struct {
	set   bool
	value string
}

func (o *originFlag) String() string   { return o.value }
func (o *originFlag) IsBoolFlag() bool { return true }
func (o *originFlag) Set(value string) error {
	o.set = true
	switch value {
	case "true":
		o.value = ""
		return nil
	case "false":
		return fmt.Errorf("--origin=false is not supported")
	default:
		o.value = value
		return nil
	}
}
