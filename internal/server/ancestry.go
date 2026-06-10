package server

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

type ProcessInfo struct {
	PID     int
	PPID    int
	Command string
}

func walkAncestry(pid int) ([]ProcessInfo, error) {
	var chain []ProcessInfo
	curr := pid
	for curr > 1 {
		cmd := exec.Command("ps", "-o", "ppid=,command=", "-p", strconv.Itoa(curr))
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			break
		}
		line := strings.TrimSpace(out.String())
		if line == "" {
			break
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			break
		}
		ppid, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			break
		}
		command := strings.TrimSpace(parts[1])
		chain = append(chain, ProcessInfo{
			PID:     curr,
			PPID:    ppid,
			Command: command,
		})
		curr = ppid
	}
	return chain, nil
}

func resolveHarnessPrincipal(pid int) (string, error) {
	chain, err := walkAncestry(pid)
	if err != nil {
		return "", err
	}
	// Walk the chain from child to parent and find a matching name.
	keywords := []string{"antigravity", "claude", "codex", "cursor", "vscode", "gemini"}
	for _, info := range chain {
		cmdLower := strings.ToLower(info.Command)
		for _, kw := range keywords {
			if strings.Contains(cmdLower, kw) {
				return kw, nil
			}
		}
	}
	// Fallback to the top-most process name in user space (just before PPID 1 or PID 1)
	if len(chain) > 0 {
		top := chain[len(chain)-1]
		parts := strings.Fields(top.Command)
		if len(parts) > 0 {
			binary := parts[0]
			if idx := strings.LastIndex(binary, "/"); idx >= 0 {
				binary = binary[idx+1:]
			}
			return strings.ToLower(binary), nil
		}
	}
	return "unknown", nil
}
