// Package history parses shell history files.
package history

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/josharian/splitby"
)

type Command struct {
	Timestamp     int64
	ExecutionTime int64
	Command       string
}

func Parse() ([]Command, error) {
	candidates := []string{
		os.Getenv("HISTFILE"),
	}
	u, err := user.Current()
	if err == nil {
		candidates = append(candidates, u.HomeDir+"/.zsh_history")
		candidates = append(candidates, u.HomeDir+"/.bash_history")
		// TODO: this is copied from the internet. test it.
		candidates = append(candidates, u.HomeDir+"/.local/share/fish")
	}
	sessions, err := filepath.Glob(u.HomeDir + "/.zsh_sessions/*.history")
	if err == nil {
		candidates = append(candidates, sessions...)
	}
	var all []Command
	var found bool
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			add, err := parseFile(candidate)
			if err == nil {
				all = append(all, add...)
			}
			found = true
		}
	}
	if found {
		return all, nil
	}
	return nil, fmt.Errorf("no history file found, tried: %v", candidates)
}

func parseFile(path string) ([]Command, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// inject a fake newline before f, to make parsing lines easier
	r := io.MultiReader(strings.NewReader("\n"), f)
	scanner := bufio.NewScanner(r)
	scanner.Split(splitby.String("\n: "))
	var all []Command
	for scanner.Scan() {
		command, err := parseLine(scanner.Bytes())
		if err != nil {
			return nil, err
		}
		if command.Command == "" {
			continue
		}
		all = append(all, command)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return all, nil
}

func parseLine(line []byte) (Command, error) {
	// fmt.Printf("line: %q\n", line)
	// sample lines (after leading ": " has been removed):
	//
	// 1647655063:0;..
	// 1647655063:0;cd ..
	// first number is timestamp, then execution time, then command
	if len(line) == 0 {
		return Command{}, nil
	}
	tsRaw, rest, ok := bytes.Cut(line, []byte(":"))
	if !ok {
		return Command{}, fmt.Errorf("invalid line: %q, expected timestamp followed by `:`", line)
	}
	etRaw, cmd, ok := bytes.Cut(rest, []byte(";"))
	if !ok {
		return Command{}, fmt.Errorf("invalid line: %q, expected execution time followed by `;`", line)
	}
	ts, err := strconv.ParseInt(string(tsRaw), 10, 64)
	if err != nil {
		return Command{}, fmt.Errorf("invalid line: %q, expected timestamp to be an integer", line)
	}
	et, err := strconv.ParseInt(string(etRaw), 10, 64)
	if err != nil {
		return Command{}, fmt.Errorf("invalid line: %q, expected execution time to be an integer", line)
	}
	return Command{
		Timestamp:     ts,
		ExecutionTime: et,
		Command:       string(bytes.TrimSpace(cmd)),
	}, nil
}
