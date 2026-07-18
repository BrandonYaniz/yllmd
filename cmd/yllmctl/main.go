package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
	gpuLayersDefault := 0
	if runtime.GOOS == "darwin" {
		gpuLayersDefault = -1
	}
	gpuLayers := flags.Int("gpu-layers", gpuLayersDefault, "runner GPU layers (-1 automatic, 0 CPU-only)")
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
		GPULayers:     *gpuLayers,
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
	if len(args) == 1 && args[0] == "installed" {
		runModelsInstalled(socketPath)
		return
	}
	if len(args) >= 1 && (args[0] == "families" || args[0] == "available") {
		runCatalogFamilies(args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "variants" {
		runCatalogVariants(modelDir, args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "choose" {
		runModelsChoose(socketPath, modelDir, args[1:])
		return
	}
	if len(args) == 1 && args[0] == "licenses" {
		runModelsLicenses(socketPath)
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
	if len(args) >= 1 && args[0] == "update" {
		runModelsUpdate(socketPath, args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "updates" {
		runModelsUpdates(socketPath, args[1:])
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
	if len(args) >= 1 && args[0] == "delete" {
		runModelsDelete(socketPath, args[1:])
		return
	}
	usage()
	os.Exit(2)
}

func runModelsInstalled(socketPath string) {
	event, err := readInstalledInventory(socketPath)
	if err != nil {
		fatal(err)
	}
	if event.Type == "error" {
		printJSON(event)
		return
	}
	if len(event.InstalledModels) == 0 {
		fmt.Println("No models are installed.")
		return
	}
	fmt.Printf("%-34s %-12s %-10s %-10s %s\n", "MODEL", "ACTIVE", "VERSIONS", "STORAGE", "CONFIGURED")
	for _, model := range event.InstalledModels {
		active := model.ActiveVersion
		if active == "" {
			active = "-"
		} else if len(active) > 12 {
			active = active[:12]
		}
		fmt.Printf("%-34s %-12s %-10d %-10s %t\n",
			model.Name, active, len(model.Versions), compatibility.FormatBytes(model.InstalledBytes), model.Configured)
	}
}

func readInstalledInventory(socketPath string) (protocol.Event, error) {
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		return protocol.Event{}, err
	}
	defer client.Close()
	if err := client.Send(protocol.Request{Type: protocol.MessageModels, ID: requestID(protocol.MessageModels), Action: "installed"}); err != nil {
		return protocol.Event{}, err
	}
	return client.ReadEvent()
}

func runModelsLicenses(socketPath string) {
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{Type: protocol.MessageModels, ID: requestID(protocol.MessageModels), Action: "licenses"}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	if event.Type == "error" {
		printJSON(event)
		return
	}
	if len(event.AcceptedLicenses) == 0 {
		fmt.Println("No model licenses have been accepted.")
		return
	}
	fmt.Printf("%-20s %-24s %-22s %s\n", "FAMILY", "LICENSE", "ACCEPTED", "TERMS")
	for _, license := range event.AcceptedLicenses {
		fmt.Printf("%-20s %-24s %-22s %s\n", license.FamilyID, license.LicenseName, license.AcceptedAt, license.TermsURL)
	}
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
	acceptance := "not required"
	if family.License.AcceptanceRequired {
		acceptance = "required before download"
	}
	fmt.Printf("License acceptance: %s\n", acceptance)
	fmt.Printf("License terms: %s\n", family.License.TermsURL)
	fmt.Printf("%s\n\n", family.Description)
	fmt.Printf("Machine: %s %s · %s RAM · %s disk available\n",
		profile.OS, profile.Architecture, formatDetectedBytes(profile.MemoryBytes), formatDetectedBytes(profile.AvailableDiskBytes))
	for _, warning := range profile.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	fmt.Println()
	fmt.Printf("%-34s %-10s %-10s %-10s %-10s %-5s %s\n", "VARIANT", "TYPE", "LEVEL", "STORAGE", "RAM", "FIT", "STATUS")
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
	fmt.Println("\nAvailable rows use qualified artifact profiles; planned rows are Q4_K_M estimates.")
}

func formatDetectedBytes(bytes uint64) string {
	if bytes == 0 {
		return "unknown"
	}
	return compatibility.FormatBytes(bytes)
}

func runModelsChoose(socketPath, modelDir string, args []string) {
	flags := flag.NewFlagSet("models choose", flag.ExitOnError)
	activate := flags.Bool("activate", false, "activate each installed variant when configured")
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
	profile, err := machine.Detect(modelDir)
	if err != nil {
		fatal(err)
	}
	variants, acceptLicense, err := guidedModelSelection(os.Stdin, os.Stdout, modelCatalog, profile)
	if err != nil {
		fatal(err)
	}
	runCatalogInstalls(socketPath, variants, acceptLicense, *activate)
}

func guidedModelSelection(input io.Reader, output io.Writer, modelCatalog catalog.Catalog, profile machine.Profile) ([]string, bool, error) {
	families := modelCatalog.SortedFamilies()
	installable := make([]catalog.Family, 0, len(families))
	for _, family := range families {
		for _, variant := range family.Variants {
			if variant.Status == "available" {
				installable = append(installable, family)
				break
			}
		}
	}
	if len(installable) == 0 {
		return nil, false, errors.New("the curated catalog has no qualified model variants")
	}

	scanner := bufio.NewScanner(input)
	fmt.Fprintf(output, "Available model families (catalog %s)\n\n", modelCatalog.CatalogVersion)
	for index, family := range installable {
		available := 0
		for _, variant := range family.Variants {
			if variant.Status == "available" {
				available++
			}
		}
		fmt.Fprintf(output, "%2d) %-18s %s (%d available)\n", index+1, family.ID, family.Name, available)
	}
	fmt.Fprint(output, "\nChoose a family by number or ID: ")
	if !scanner.Scan() {
		return nil, false, selectionReadError(scanner.Err())
	}
	family, err := selectedFamily(strings.TrimSpace(scanner.Text()), installable)
	if err != nil {
		return nil, false, err
	}

	available := make([]catalog.Variant, 0, len(family.Variants))
	fmt.Fprintf(output, "\n%s\nPublisher: %s\nOrigin: %s\nLicense: %s\nTerms: %s\n\n",
		family.Name, family.Publisher, strings.Join(family.Countries, ", "), family.License.Name, family.License.TermsURL)
	fmt.Fprintf(output, "%-4s %-34s %-10s %-10s %-5s\n", "#", "VARIANT", "STORAGE", "RAM", "FIT")
	for _, variant := range family.Variants {
		if variant.Status != "available" {
			continue
		}
		available = append(available, variant)
		assessment, err := compatibility.Assess(variant, profile)
		if err != nil {
			return nil, false, err
		}
		fit := "unknown"
		if assessment.CompatibilityKnown {
			fit = "yes"
			if !assessment.Compatible {
				fit = "no"
			}
		}
		fmt.Fprintf(output, "%-4d %-34s %-10s %-10s %-5s\n", len(available), variant.ID,
			compatibility.FormatBytes(assessment.Requirements.StorageBytes),
			compatibility.FormatBytes(assessment.Requirements.RecommendedRAM), fit)
	}
	fmt.Fprint(output, "\nChoose one or more variants (comma-separated numbers or IDs), or all: ")
	if !scanner.Scan() {
		return nil, false, selectionReadError(scanner.Err())
	}
	selected, err := selectedVariants(strings.TrimSpace(scanner.Text()), available)
	if err != nil {
		return nil, false, err
	}

	acceptLicense := false
	if family.License.AcceptanceRequired {
		fmt.Fprintf(output, "Accept %s at %s? [y/N]: ", family.License.Name, family.License.TermsURL)
		if !scanner.Scan() {
			return nil, false, selectionReadError(scanner.Err())
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer != "y" && answer != "yes" {
			return nil, false, errors.New("license terms were not accepted")
		}
		acceptLicense = true
	}
	return selected, acceptLicense, nil
}

func selectedFamily(choice string, families []catalog.Family) (catalog.Family, error) {
	if number, err := strconv.Atoi(choice); err == nil {
		if number >= 1 && number <= len(families) {
			return families[number-1], nil
		}
		return catalog.Family{}, fmt.Errorf("family number %d is out of range", number)
	}
	for _, family := range families {
		if family.ID == choice {
			return family, nil
		}
	}
	return catalog.Family{}, fmt.Errorf("model family %q is not an available curated family", choice)
}

func selectedVariants(choice string, variants []catalog.Variant) ([]string, error) {
	if strings.EqualFold(choice, "all") {
		selected := make([]string, len(variants))
		for index, variant := range variants {
			selected[index] = variant.ID
		}
		return selected, nil
	}
	parts := strings.FieldsFunc(choice, func(character rune) bool {
		return character == ',' || character == ' ' || character == '\t'
	})
	if len(parts) == 0 {
		return nil, errors.New("choose at least one model variant")
	}
	selected := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		variantID := part
		if number, err := strconv.Atoi(part); err == nil {
			if number < 1 || number > len(variants) {
				return nil, fmt.Errorf("variant number %d is out of range", number)
			}
			variantID = variants[number-1].ID
		} else {
			found := false
			for _, variant := range variants {
				if variant.ID == variantID {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("model variant %q is not available in the selected family", variantID)
			}
		}
		if _, exists := seen[variantID]; exists {
			return nil, fmt.Errorf("variant %q was selected more than once", variantID)
		}
		seen[variantID] = struct{}{}
		selected = append(selected, variantID)
	}
	return selected, nil
}

func selectionReadError(err error) error {
	if err != nil {
		return fmt.Errorf("read model selection: %w", err)
	}
	return errors.New("model selection ended before a choice was entered")
}

func runModelsInstall(socketPath string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	model := args[0]
	installFlags := flag.NewFlagSet("models install", flag.ExitOnError)
	var variantIDs repeatedStrings
	installFlags.Var(&variantIDs, "variant", "variant within the selected family (repeatable)")
	allVariants := installFlags.Bool("all", false, "install every available variant in the selected family")
	file := installFlags.String("file", "", "local GGUF file to install")
	version := installFlags.String("version", "", "model version id")
	sha256 := installFlags.String("sha256", "", "expected SHA-256 checksum")
	activate := installFlags.Bool("activate", false, "activate the installed version")
	acceptLicense := installFlags.Bool("accept-license", false, "explicitly accept the model family's license terms")
	if err := installFlags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(installFlags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	activateWasSet := false
	installFlags.Visit(func(flag *flag.Flag) {
		if flag.Name == "activate" {
			activateWasSet = true
		}
	})
	if *file == "" {
		if *version != "" || *sha256 != "" {
			fatal(fmt.Errorf("version and sha256 are only valid with -file"))
		}
		selected, err := catalogInstallSelection(model, variantIDs, *allVariants)
		if err != nil {
			fatal(err)
		}
		runCatalogInstalls(socketPath, selected, *acceptLicense, *activate)
		return
	}
	if len(variantIDs) != 0 || *allVariants {
		fatal(fmt.Errorf("-variant and -all cannot be combined with -file"))
	}
	if *acceptLicense {
		fatal(fmt.Errorf("-accept-license is only valid for curated catalog installs"))
	}
	if !activateWasSet {
		*activate = true
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

func catalogInstallSelection(id string, requested []string, all bool) ([]string, error) {
	modelCatalog, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	if family, ok := modelCatalog.Family(id); ok {
		if all && len(requested) != 0 {
			return nil, fmt.Errorf("choose either -all or one or more -variant values")
		}
		if all {
			selected := make([]string, 0, len(family.Variants))
			for _, variant := range family.Variants {
				if variant.Status == "available" {
					selected = append(selected, variant.ID)
				}
			}
			if len(selected) == 0 {
				return nil, fmt.Errorf("model family %q has no qualified variants yet", id)
			}
			return selected, nil
		}
		if len(requested) == 0 {
			return nil, fmt.Errorf("model family %q requires -variant or -all", id)
		}
		familyVariants := make(map[string]struct{}, len(family.Variants))
		for _, variant := range family.Variants {
			familyVariants[variant.ID] = struct{}{}
		}
		seen := make(map[string]struct{}, len(requested))
		for _, variantID := range requested {
			if _, ok := familyVariants[variantID]; !ok {
				return nil, fmt.Errorf("variant %q does not belong to model family %q", variantID, id)
			}
			if _, exists := seen[variantID]; exists {
				return nil, fmt.Errorf("variant %q was selected more than once", variantID)
			}
			seen[variantID] = struct{}{}
		}
		return append([]string(nil), requested...), nil
	}
	if len(requested) != 0 || all {
		return nil, fmt.Errorf("%q is not a model family", id)
	}
	if _, _, ok := modelCatalog.Variant(id); !ok {
		return nil, fmt.Errorf("model variant %q is not in the curated catalog", id)
	}
	return []string{id}, nil
}

func runCatalogInstalls(socketPath string, variants []string, acceptLicense, activate bool) {
	runCatalogActions(socketPath, "download", variants, acceptLicense, activate)
}

func runModelsUpdate(socketPath string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	if args[0] == "-all" || args[0] == "--all" {
		updateFlags := flag.NewFlagSet("models update -all", flag.ExitOnError)
		updateFlags.Bool("all", false, "update every installed curated variant with a newer catalog revision")
		activate := updateFlags.Bool("activate", false, "activate qualified revisions for configured models")
		acceptLicense := updateFlags.Bool("accept-license", false, "explicitly accept required model-family license terms")
		if err := updateFlags.Parse(args); err != nil {
			fatal(err)
		}
		if len(updateFlags.Args()) != 0 {
			usage()
			os.Exit(2)
		}
		runModelsUpdateAll(socketPath, *acceptLicense, *activate)
		return
	}
	variantID := args[0]
	updateFlags := flag.NewFlagSet("models update", flag.ExitOnError)
	activate := updateFlags.Bool("activate", false, "activate the qualified catalog revision")
	acceptLicense := updateFlags.Bool("accept-license", false, "explicitly accept the model family's license terms")
	if err := updateFlags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(updateFlags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		fatal(err)
	}
	if _, _, ok := modelCatalog.Variant(variantID); !ok {
		fatal(fmt.Errorf("model variant %q is not in the curated catalog", variantID))
	}
	runCatalogActions(socketPath, "update", []string{variantID}, *acceptLicense, *activate)
}

type catalogUpdateStatus struct {
	Model            string `json:"model"`
	ActiveVersion    string `json:"active_version,omitempty"`
	CatalogVersion   string `json:"catalog_version"`
	Configured       bool   `json:"configured"`
	UpdateAvailable  bool   `json:"update_available"`
	CatalogInstalled bool   `json:"catalog_installed"`
}

func runModelsUpdates(socketPath string, args []string) {
	flags := flag.NewFlagSet("models updates", flag.ExitOnError)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		fatal(err)
	}
	if len(flags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	rows, err := installedCatalogUpdates(socketPath)
	if err != nil {
		fatal(err)
	}
	if *jsonOutput {
		printJSON(rows)
		return
	}
	if len(rows) == 0 {
		fmt.Println("No curated catalog models are installed.")
		return
	}
	fmt.Printf("%-34s %-12s %-12s %-16s\n", "MODEL", "ACTIVE", "CATALOG", "STATUS")
	for _, row := range rows {
		status := "up to date"
		if row.UpdateAvailable {
			status = "update available"
		} else if row.ActiveVersion != row.CatalogVersion {
			status = "downloaded"
		}
		fmt.Printf("%-34s %-12s %-12s %-16s\n", row.Model, shortVersion(row.ActiveVersion), shortVersion(row.CatalogVersion), status)
	}
}

func runModelsUpdateAll(socketPath string, acceptLicense, activate bool) {
	rows, err := installedCatalogUpdates(socketPath)
	if err != nil {
		fatal(err)
	}
	plans := pendingCatalogUpdatePlans(rows, activate)
	if len(plans) == 0 {
		fmt.Println("All installed curated models are up to date.")
		return
	}
	runCatalogActionPlans(socketPath, "update", plans, acceptLicense)
}

func installedCatalogUpdates(socketPath string) ([]catalogUpdateStatus, error) {
	event, err := readInstalledInventory(socketPath)
	if err != nil {
		return nil, err
	}
	if event.Type == "error" {
		return nil, fmt.Errorf("%s: %s", event.Code, event.Message)
	}
	if event.Type != "installed_models" {
		return nil, fmt.Errorf("unexpected installed-model inventory response %q", event.Type)
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	return catalogUpdateStatuses(modelCatalog, event.InstalledModels), nil
}

func catalogUpdateStatuses(modelCatalog catalog.Catalog, installed []protocol.InstalledModel) []catalogUpdateStatus {
	rows := make([]catalogUpdateStatus, 0, len(installed))
	for _, model := range installed {
		_, variant, ok := modelCatalog.Variant(model.Name)
		if !ok || variant.Status != "available" || variant.Artifact == nil {
			continue
		}
		catalogInstalled := false
		for _, version := range model.Versions {
			if version.Version == variant.Artifact.Revision {
				catalogInstalled = true
				break
			}
		}
		rows = append(rows, catalogUpdateStatus{
			Model: model.Name, ActiveVersion: model.ActiveVersion,
			CatalogVersion: variant.Artifact.Revision, Configured: model.Configured,
			UpdateAvailable: !catalogInstalled, CatalogInstalled: catalogInstalled,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })
	return rows
}

type catalogActionPlan struct {
	variant  string
	activate bool
}

func pendingCatalogUpdatePlans(rows []catalogUpdateStatus, activate bool) []catalogActionPlan {
	plans := make([]catalogActionPlan, 0, len(rows))
	for _, row := range rows {
		if row.UpdateAvailable || (activate && row.Configured && row.ActiveVersion != row.CatalogVersion) {
			plans = append(plans, catalogActionPlan{variant: row.Model, activate: activate && row.Configured})
		}
	}
	return plans
}

func shortVersion(version string) string {
	if version == "" {
		return "-"
	}
	if len(version) > 12 {
		return version[:12]
	}
	return version
}

func runCatalogActions(socketPath, action string, variants []string, acceptLicense, activate bool) {
	plans := make([]catalogActionPlan, 0, len(variants))
	for _, variant := range variants {
		plans = append(plans, catalogActionPlan{variant: variant, activate: activate})
	}
	runCatalogActionPlans(socketPath, action, plans, acceptLicense)
}

func runCatalogActionPlans(socketPath, action string, plans []catalogActionPlan, acceptLicense bool) {
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	for _, plan := range plans {
		variant := plan.variant
		id := requestID(protocol.MessageModels)
		if err := client.Send(protocol.Request{
			Type: protocol.MessageModels, ID: id, Action: action, Model: variant,
			Activate: &plan.activate, LicenseAccepted: acceptLicense,
		}); err != nil {
			fatal(err)
		}
		for {
			event, err := client.ReadEvent()
			if err != nil {
				fatal(err)
			}
			switch event.Type {
			case "download_progress":
				percent := float64(0)
				if event.TotalBytes > 0 {
					percent = float64(event.DownloadedBytes) * 100 / float64(event.TotalBytes)
				}
				fmt.Fprintf(os.Stderr, "\rDownloading %s: %.1f%%", variant, percent)
			case "installed", "updated", "up_to_date":
				fmt.Fprintln(os.Stderr)
				printJSON(event)
				goto nextVariant
			case "error", "cancelled":
				fmt.Fprintln(os.Stderr)
				printJSON(event)
				return
			}
		}
	nextVariant:
	}
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

func runModelsDelete(socketPath string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	model := args[0]
	deleteFlags := flag.NewFlagSet("models delete", flag.ExitOnError)
	version := deleteFlags.String("version", "", "delete only this installed version")
	yes := deleteFlags.Bool("yes", false, "delete without an interactive confirmation")
	if err := deleteFlags.Parse(args[1:]); err != nil {
		fatal(err)
	}
	if len(deleteFlags.Args()) != 0 {
		usage()
		os.Exit(2)
	}
	if !*yes {
		target := fmt.Sprintf("model %s and all of its installed versions", model)
		if *version != "" {
			target = fmt.Sprintf("version %s of model %s", *version, model)
		}
		fmt.Fprintf(os.Stderr, "Delete %s? [y/N] ", target)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return
		}
	}
	client, err := ipc.Dial(socketPath, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	if err := client.Send(protocol.Request{
		Type: protocol.MessageModels, ID: requestID(protocol.MessageModels), Action: "delete", Model: model, Version: *version,
	}); err != nil {
		fatal(err)
	}
	event, err := client.ReadEvent()
	if err != nil {
		fatal(err)
	}
	if event.Type == "deleted" {
		fmt.Fprintf(os.Stderr, "Reclaimed %s.\n", compatibility.FormatBytes(event.ReclaimedBytes))
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
	temperature := generateFlags.Float64("temperature", 0.8, "sampling temperature")
	topP := generateFlags.Float64("top-p", 0.95, "top-p sampling threshold")
	topK := generateFlags.Int("top-k", 40, "top-k sampling limit; 0 disables it")
	minP := generateFlags.Float64("min-p", 0.05, "minimum probability sampling threshold")
	presencePenalty := generateFlags.Float64("presence-penalty", 0, "presence penalty from -2 to 2")
	repeatPenalty := generateFlags.Float64("repeat-penalty", 1, "repeat penalty greater than zero")
	seed := generateFlags.Uint64("seed", math.MaxUint64, "random seed; max uint64 selects runner randomness")
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
			MaxTokens: maxTokens, Temperature: temperature, TopP: topP,
			TopK: topK, MinP: minP, PresencePenalty: presencePenalty,
			RepeatPenalty: repeatPenalty, Seed: seed,
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
	fmt.Fprintf(os.Stderr, "usage: yllmctl [-mode user|daemon] [-socket path] <config create -variant id [-variant id]|health|status|providers|models families|models variants family|models choose|models licenses|models installed|models updates|models list|models versions model|models install variant|models install family -variant id [-variant id]|models install family -all|models install model -file path -version id -sha256 hash|models update variant [-activate]|models update -all [-activate]|models activate model -version id|models rollback model|models delete model [-version id] [-yes]|cancel id|generate>\n")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
