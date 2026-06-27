package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/ipc"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

var version = "dev"

func main() {
	socketPath := flag.String("socket", "/var/run/yllmd/yllmd.sock", "path to yllmd Unix socket")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
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
		runModels(*socketPath, args[1:])
	case "cancel":
		runCancel(*socketPath, args[1:])
	case "generate":
		runGenerate(*socketPath, args[1:])
	default:
		usage()
		os.Exit(2)
	}
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

func runModels(socketPath string, args []string) {
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
	model := generateFlags.String("model", "fast", "model tier or alias")
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
		Type:     protocol.MessageGenerate,
		ID:       id,
		Provider: "local",
		Model:    *model,
		Stream:   stream,
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
	fmt.Fprintf(os.Stderr, "usage: yllmctl [-socket path] <health|status|providers|models list|models versions model|models install model -file path -version id -sha256 hash|models activate model -version id|models rollback model|cancel id|generate>\n")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
