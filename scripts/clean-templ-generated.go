package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var templChildVar = regexp.MustCompile(`^templ_[A-Za-z0-9]+_Var[0-9]+$`)

func main() {
	paths := os.Args[1:]
	if len(paths) > 0 && paths[0] == "--" {
		paths = paths[1:]
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: clean-templ-generated <file> [file...]")
		os.Exit(2)
	}

	var failed bool
	for _, path := range paths {
		if err := cleanFile(path); err != nil {
			// #nosec G705 -- this CLI reports local generator paths to stderr only.
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func cleanFile(path string) error {
	// #nosec G304 G703 -- this generator cleanup is invoked with checked-in templ output paths.
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	original := string(contents)
	lines, finalNewline := splitLines(original)

	lines = removeUnusedChildAssignments(lines)
	lines = discardTerminalClearChildrenAssignments(lines)

	cleaned := joinLines(lines, finalNewline)
	if cleaned == original {
		return nil
	}
	// #nosec G306 G703 -- generated Go source should keep normal repository file permissions.
	return os.WriteFile(path, []byte(cleaned), 0o644)
}

func splitLines(contents string) ([]string, bool) {
	finalNewline := strings.HasSuffix(contents, "\n")
	lines := strings.Split(contents, "\n")
	if finalNewline {
		lines = lines[:len(lines)-1]
	}
	return lines, finalNewline
}

func joinLines(lines []string, finalNewline bool) string {
	contents := strings.Join(lines, "\n")
	if finalNewline {
		contents += "\n"
	}
	return contents
}

func removeUnusedChildAssignments(lines []string) []string {
	cleaned := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		variable, ok := childAssignmentVariable(lines[index])
		if ok && isUnusedChildAssignmentBlock(lines, index, variable) {
			index += 4
			continue
		}
		cleaned = append(cleaned, lines[index])
		index++
	}
	return cleaned
}

func childAssignmentVariable(line string) (string, bool) {
	const (
		prefix = "\t\t"
		suffix = " := templ.GetChildren(ctx)"
	)
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return "", false
	}
	variable := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	return variable, templChildVar.MatchString(variable)
}

func isUnusedChildAssignmentBlock(lines []string, index int, variable string) bool {
	return index+3 < len(lines) &&
		lines[index+1] == "\t\tif "+variable+" == nil {" &&
		lines[index+2] == "\t\t\t"+variable+" = templ.NopComponent" &&
		lines[index+3] == "\t\t}"
}

func discardTerminalClearChildrenAssignments(lines []string) []string {
	cleaned := make([]string, 0, len(lines))
	for index, line := range lines {
		if line == "\t\tctx = templ.ClearChildren(ctx)" && !usesContextBeforeTemplateReturn(lines, index+1) {
			cleaned = append(cleaned, "\t\ttempl.ClearChildren(ctx)")
			continue
		}
		cleaned = append(cleaned, line)
	}
	return cleaned
}

func usesContextBeforeTemplateReturn(lines []string, index int) bool {
	for ; index < len(lines); index++ {
		if strings.Contains(lines[index], "ctx") {
			return true
		}
		if lines[index] == "\t\treturn nil" {
			return false
		}
	}
	return true
}
