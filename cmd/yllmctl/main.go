package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/compatibility"
	"github.com/BrandonYaniz/yllmd/internal/configgen"
	"github.com/BrandonYaniz/yllmd/internal/ipc"
	"github.com/BrandonYaniz/yllmd/internal/locations"
	"github.com/BrandonYaniz/yllmd/internal/machine"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

var version = "dev"

func main() {
	mode := flag.String("mode", string(locations.ModeUser), "operating mode: user or daemon")
	socketPath := flag.String("socket", "", "path to yllmd Unix socket (overrides mode default)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(fmt.Errorf("resolve home directory: %w", err))
	}
	paths, err := locations.Resolve(locations.Mode(*mode), runtime.GOOS, runtime.GOARCH, home)
	if err != nil {
		fatal(err)
	}
	if *socketPath == "" {
		*socketPath = paths.SocketPath
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "health":
		runSingle(*socketPath, protocol.MessageHealth)
	case "status":
		runSingle(*socketPath, protocol.MessageStatus)
	case "providers":
		runSingle(*socketPath, protocol.MessageProviders)
	case "models":
		runModels(*socketPath, paths.ModelDir, args[1:])
	case "config":
		runConfig(locations.Mode(*mode), paths, args[1:])
	case "cancel":
		runCancel(*socketPath, args[1:])
	case "generate":
		runGenerate(*socketPath, args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

type repeatedStrings []string

func (values *repeatedStrings) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStrings) Set(value string) error {
	if value == "" {
		return fmt.Errorf("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func runConfig(mode locations.Mode, paths locations.Paths, args []string) {
	if len(args) == 0 || args[0] != "create" {
		usage()
		os.Exit(2)
	}
	flags := flag.NewFlagSet("config create", flag.ExitOnError)
	var variantIDs repeatedStrings
	flags.Var(&variantIDs, "variant", "catalog variant ID to assign (repeatable)")
	residentID := flags.String("resident", "", "selected variant to keep resident")
	runnerCommand := flags.String("runner", "yllama-runner", "yllama runner command")
	threads := flags.Int("threads", runtime.NumCPU(), "runner thread count")
	output := flags.String("output", paths.ConfigFile, "configuration output path")
	force := flags.Bool("force", false, "replace an existing configuration")
	if err := flags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(flags.Args()) != 0 || len(variantIDs) == 0 {
		usage()
		os.Exit(2)
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		fatal(err)
	}
	variants := make([]catalog.Variant, 0, len(variantIDs))
	seen := make(map[string]struct{}, len(variantIDs))
	for _, id := range variantIDs {
		if _, exists := seen[id]; exists {
			fatal(fmt.Errorf("variant %q was selected more than once", id))
		}
		seen[id] = struct{}{}
		_, variant, ok := modelCatalog.Variant(id)
		if !ok {
			fatal(fmt.Errorf("model variant %q is not in the curated catalog", id))
		}
		variants = append(variants, variant)
	}
	data, err := configgen.Generate(configgen.Options{
		Mode:          mode,
		Paths:         paths,
		Variants:      variants,
		ResidentID:    *residentID,
		RunnerCommand: *runnerCommand,
		Threads:       *threads,
	})
	if err != nil {
		fatal(err)
	}
	if err := writeConfig(*output, data, *force, mode); err != nil {
		fatal(err)
	}
	fmt.Printf("Wrote %s configuration to %s\n", mode, *output)
	fmt.Println("Selected catalog variants are planned; install qualified artifacts before starting yllmd.")
}

func writeConfig(path string, data []byte, force bool, mode locations.Mode) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("configuration already exists at %s (use -force to replace it)", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	directoryMode := os.FileMode(0o700)
	if mode == locations.ModeDaemon {
		directoryMode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install configuration: %w", err)
	}
	return nil
}

func runSingle(socketPath string, messageType protocol.MessageType) {
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()

	if err := client.Send(protocol.Request{Type: messageType, ID: requestID(messageType)}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runModels(socketPath, modelDir string, args []string) {
	if len(args) >= 1 && (args[0] == "families" || args[0] == "available") {
		runCatalogFamilies(args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "variants" {
		runCatalogVariants(modelDir, args[1:])
		return
	}
	if len(args) == 1 && args[0] == "list" {
		runSingle(socketPath, protocol.MessageModels)
		return
	}
	if len(args) >= 1 && args[0] == "install" {
		runModelsInstall(socketPath, args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "activate" {
		runModelsActivate(socketPath, args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "versions" {
		runModelsVersions(socketPath, args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "rollback" {
		runModelsRollback(socketPath, args[1:])
		return
	}
	usage()
	os.Exit(2)
}

func runCatalogFamilies(args []string) {
	flags := flag.NewFlagSet("models families", flag.ExitOnError)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		fatal(err)
	}
	if len(flags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		fatal(err)
	}
	if *jsonOutput {
		printJSON(modelCatalog.SortedFamilies())
		return
	}
	fmt.Printf("Curated model families (catalog %s)\n\n", modelCatalog.CatalogVersion)
	for _, family := range modelCatalog.SortedFamilies() {
		fmt.Printf("%-18s  %s\n", family.ID, family.Name)
		fmt.Printf("%-18s  %s · %s · %d variants\n", "", strings.Join(family.Countries, ", "), family.License.Name, len(family.Variants))
	}
}

func runCatalogVariants(modelDir string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	familyID := args[0]
	flags := flag.NewFlagSet("models variants", flag.ExitOnError)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(flags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		fatal(err)
	}
	family, ok := modelCatalog.Family(familyID)
	if !ok {
		fatal(fmt.Errorf("model family %q is not in the curated catalog", familyID))
	}
	profile, err := machine.Detect(modelDir)
	if err != nil {
		fatal(err)
	}
	assessments := make([]compatibility.Assessment, 0, len(family.Variants))
	for _, variant := range family.Variants {
		assessment, err := compatibility.Assess(variant, profile)
		if err != nil {
			fatal(err)
		}
		assessments = append(assessments, assessment)
	}
	if *jsonOutput {
		printJSON(struct {
			Family      catalog.Family             `json:"family"`
			Machine     machine.Profile            `json:"machine"`
			Assessments []compatibility.Assessment `json:"assessments"`
		}{Family: family, Machine: profile, Assessments: assessments})
		return
	}
	fmt.Printf("%s\n", family.Name)
	fmt.Printf("Publisher: %s\n", family.Publisher)
	fmt.Printf("Origin: %s\n", strings.Join(family.Countries, ", "))
	fmt.Printf("License: %s\n", family.License.Name)
	fmt.Printf("%s\n\n", family.Description)
	fmt.Printf("Machine: %s %s · %s RAM · %s disk available\n",
		profile.OS, profile.Architecture, formatDetectedBytes(profile.MemoryBytes), formatDetectedBytes(profile.AvailableDiskBytes))
	for _, warning := range profile.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	fmt.Println()
	fmt.Printf("%-34s %-10s %-10s %-10s %-10s %-5s %s\n", "VARIANT", "TYPE", "LEVEL", "STORAGE*", "RAM*", "FIT", "STATUS")
	for _, assessment := range assessments {
		fit := "unknown"
		if assessment.CompatibilityKnown {
			fit = "yes"
		}
		if assessment.CompatibilityKnown && !assessment.Compatible {
			fit = "no"
		}
		fmt.Printf("%-34s %-10s %-10s %-10s %-10s %-5s %s\n",
			assessment.Variant.ID,
			assessment.Variant.ModelType,
			assessment.Variant.Level,
			compatibility.FormatBytes(assessment.Requirements.StorageBytes),
			compatibility.FormatBytes(assessment.Requirements.RecommendedRAM),
			fit,
			assessment.Variant.Status,
		)
		if !assessment.Compatible {
			fmt.Printf("  %s\n", strings.Join(assessment.Reasons, "; "))
		}
	}
	fmt.Println("\n* Estimated for the planned Q4_K_M artifact; qualified measurements will replace estimates.")
}

func formatDetectedBytes(bytes uint64) string {
	if bytes == 0 {
		return "unknown"
	}
	return compatibility.FormatBytes(bytes)
}

func runModelsInstall(socketPath string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	model := args[0]
	installFlags := flag.NewFlagSet("models install", flag.ExitOnError)
	file := installFlags.String("file", "", "local GGUF file to install")
	version := installFlags.String("version", "", "model version id")
	sha256 := installFlags.String("sha256", "", "expected SHA-256 checksum")
	activate := installFlags.Bool("activate", true, "activate the installed version")
	if err := installFlags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(installFlags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{
		Type:     protocol.MessageModels,
		ID:       requestID(protocol.MessageModels),
		Action:   "install",
		Model:    model,
		Version:  *version,
		File:     *file,
		SHA256:   *sha256,
		Activate: activate,
	}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runModelsActivate(socketPath string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	model := args[0]
	activateFlags := flag.NewFlagSet("models activate", flag.ExitOnError)
	version := activateFlags.String("version", "", "model version id")
	if err := activateFlags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(activateFlags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{
		Type:    protocol.MessageModels,
		ID:      requestID(protocol.MessageModels),
		Action:  "activate",
		Model:   model,
		Version: *version,
	}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runModelsVersions(socketPath string, args []string) {
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{
		Type:   protocol.MessageModels,
		ID:     requestID(protocol.MessageModels),
		Action: "versions",
		Model:  args[0],
	}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runModelsRollback(socketPath string, args []string) {
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{
		Type:   protocol.MessageModels,
		ID:     requestID(protocol.MessageModels),
		Action: "rollback",
		Model:  args[0],
	}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runCancel(socketPath string, args []string) {
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{Type: protocol.MessageCancel, ID: args[0]}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	printJSON(event)
}

func runGenerate(socketPath string, args []string) {
	generateFlags := flag.NewFlagSet("generate", flag.ExitOnError)
	model := generateFlags.String("model", "", "model name, tier, or alias")
	modelType := generateFlags.String("model-type", "", "model type: llm or code")
	level := generateFlags.String("level", "", "model level: fast, balanced, or deep")
	prompt := generateFlags.String("prompt", "", "prompt text")
	stream := generateFlags.Bool("stream", true, "stream text deltas")
	output := generateFlags.String("output", "json", "output format: json or text")
	maxTokens := generateFlags.Int("max-tokens", 128, "maximum output tokens")
	if err := generateFlags.Parse(args); err != nil {
		fatal(err)
	}
	outputFormat := normalizeOutputFormat(*output)
	if outputFormat == "" {
		fatal(fmt.Errorf("unsupported output format %q", *output))
	}
	if *prompt == "" {
		remaining := generateFlags.Args()
		if len(remaining) > 0 {
			*prompt = remaining[0]
		}
	}
	if *prompt == "" {
		fatal(fmt.Errorf("generate requires -prompt or a prompt argument"))
	}

	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()

	id := requestID(protocol.MessageGenerate)
	request := protocol.Request{
		Type:      protocol.MessageGenerate,
		ID:        id,
		Provider:  "local",
		Model:     *model,
		ModelType: *modelType,
		Level:     *level,
		Stream:    stream,
		Input: &protocol.Input{
			Kind:   "prompt",
			Prompt: *prompt,
		},
		Settings: protocol.GenerationSettings{
			MaxTokens: maxTokens,
			Output: &protocol.Output{
				Format:   outputFormat,
				Delivery: outputDelivery(*stream),
			},
		},
	}
	if err := client.Send(request); err != nil {
		fatal(err)
	}
	if outputFormat == "text" {
		if err := client.ReadRaw(os.Stdout); err != nil {
			fatal(err)
		}
		return
	}
	for {
		event, err := client.ReadEvent()
		if err != nil {
			fatal(err)
		}
		if *stream {
			printJSONLine(event)
		}
		switch event.Type {
		case "completed", "error", "cancelled":
			if !*stream {
				printJSON(event)
			}
			return
		}
	}
}

func outputDelivery(stream bool) string {
	if stream {
		return "stream"
	}
	return "complete"
}

func normalizeOutputFormat(format string) string {
	switch format {
	case "", "json":
		return "json"
	case "text", "raw":
		return "text"
	default:
		return ""
	}
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
}

func printJSONLine(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
}

func requestID(kind protocol.MessageType) string {
	return fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: yllmctl [-mode user|daemon] [-socket path] <config create -variant id [-variant id]|health|status|providers|models families|models variants family|models list|models versions model|models install model -file path -version id -sha256 hash|models activate model -version id|models rollback model|cancel id|generate>\n")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
