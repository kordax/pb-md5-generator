package content

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// validCommand is the complete subprocess protocol used by this backend.
// Repository data cannot select an executable, a Git verb, or additional flags.
func (g *GitBackend) validCommand(input string, arguments []string) bool {
	if len(arguments) == 0 || input != "" && arguments[0] != "add" {
		return false
	}
	match := func(expected ...string) bool { return slices.Equal(arguments, expected) }
	switch arguments[0] {
	case "rev-parse":
		return match("rev-parse", "--show-toplevel") || match("rev-parse", "--verify", "HEAD")
	case "symbolic-ref":
		return match("symbolic-ref", "--quiet", "--short", "HEAD")
	case "status":
		return match("status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	case "config":
		return match("config", "--local", "--no-includes", "--name-only", "--list")
	case "remote":
		return match("remote", "get-url", "--all", g.remote) || match("remote", "get-url", "--push", "--all", g.remote)
	case "rev-list":
		return match("rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	case "for-each-ref":
		return match("for-each-ref", "--format=%(refname:short)%00%(objectname)", "refs/heads/")
	case "check-ref-format":
		return len(arguments) == 3 && arguments[1] == "--branch" && gitBranchArgument(arguments[2])
	case "branch":
		return len(arguments) == 3 && arguments[1] == "--" && gitBranchArgument(arguments[2])
	case "checkout":
		return len(arguments) == 4 && arguments[1] == "--no-guess" && gitBranchArgument(arguments[2]) && arguments[3] == "--"
	case "fetch":
		return match("fetch", "--no-recurse-submodules", "--", g.remote)
	case "push":
		return len(arguments) == 5 && slices.Equal(arguments[:4], []string{"push", "--set-upstream", "--", g.remote}) && strings.HasPrefix(arguments[4], "HEAD:refs/heads/") && gitBranchArgument(strings.TrimPrefix(arguments[4], "HEAD:refs/heads/"))
	case "merge":
		return len(arguments) == 5 && slices.Equal(arguments[:4], []string{"merge", "--ff-only", "--no-edit", "--"}) && strings.HasPrefix(arguments[4], g.remote+"/") && gitBranchArgument(strings.TrimPrefix(arguments[4], g.remote+"/"))
	case "reset":
		if len(arguments) != 3 || arguments[1] != "--keep" || len(arguments[2]) != 40 && len(arguments[2]) != 64 {
			return false
		}
		_, err := hexDecode(arguments[2])
		return err == nil
	case "add":
		if !match("add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul") || input == "" || len(input) > MaxFiles*1025 || !strings.HasSuffix(input, "\x00") {
			return false
		}
		paths := strings.Split(strings.TrimSuffix(input, "\x00"), "\x00")
		if len(paths) > MaxFiles {
			return false
		}
		for _, path := range paths {
			if !allowedFile(path) {
				return false
			}
		}
		return true
	case "diff":
		return match("diff", "--cached", "--name-only", "-z")
	case "-c":
		return len(arguments) == 10 && arguments[2] == "-c" && strings.HasPrefix(arguments[1], "user.name=") && strings.HasPrefix(arguments[3], "user.email=") && slices.Equal(arguments[4:8], []string{"commit", "--no-verify", "--no-gpg-sign", "-m"}) && arguments[9] == "--" && gitText(strings.TrimPrefix(arguments[1], "user.name="), 256) && gitText(strings.TrimPrefix(arguments[3], "user.email="), 320) && gitText(arguments[8], 256)
	default:
		return false
	}
}

func gitText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func gitBranchArgument(value string) bool {
	return gitText(value, 128) && !strings.HasPrefix(value, "-")
}
