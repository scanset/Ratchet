package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/scanset/Ratchet/internal/conventions"
	"github.com/scanset/Ratchet/internal/dispatch"
	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/ollama"
)

// runConsole is the operator console: a thin REPL over a Dispatcher. The conversation, routing, and
// oracle logic all live in the Dispatcher; this is just stdin/stdout plumbing. Port of ConsoleChat.cs.
func runConsole(inst *instance.Instance, url string) {
	fmt.Println("ratchet operator console - '" + inst.Config.Name + "'")
	fmt.Println("  dispatch seat: " + inst.Config.DispatchModel() +
		"   generate seat: " + inst.Config.Models.Generate + "   ollama: " + url)
	fmt.Println("  just type what you want - it's matched to a workflow (you confirm before it runs),")
	fmt.Println("  or use slash commands directly. '/help' lists them, '/flows' shows workflows, 'quit' exits.")
	fmt.Println()

	d := dispatch.New(inst, url, func(s string) { fmt.Fprintln(os.Stderr, "  - "+s) })

	firstToken := false
	d.OnToken = func(t string) {
		if firstToken {
			fmt.Println()
			firstToken = false
		}
		fmt.Print(t)
	}

	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("ratchet > ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			break // EOF
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "quit" || line == "exit" || line == ":q" {
			break
		}

		firstToken = true
		pTok, eTok, pCalls := ollama.MeterPrompt(), ollama.MeterEval(), ollama.MeterCalls()
		r := d.Turn(line)
		if r.Intent == conventions.IntentQuit {
			break
		}
		if r.Intent == "clear" {
			fmt.Print("\033[H\033[2J")
			continue
		}
		switch {
		case r.Streamed:
			fmt.Println()
			fmt.Println()
		case r.IsError:
			fmt.Fprintln(os.Stderr, "\n"+r.Text+"\n")
		default:
			fmt.Println("\n" + r.Text + "\n")
		}

		if dCalls := ollama.MeterCalls() - pCalls; dCalls > 0 {
			fmt.Printf("  [local model: %d generated + %d prompt tok, %d call(s)]\n\n",
				ollama.MeterEval()-eTok, ollama.MeterPrompt()-pTok, dCalls)
		}
	}
	if ollama.MeterCalls() > 0 {
		fmt.Printf("session local tokens: %d generated + %d prompt = %d across %d call(s)\n",
			ollama.MeterEval(), ollama.MeterPrompt(), ollama.MeterTotal(), ollama.MeterCalls())
	}
	fmt.Println("bye")
}
