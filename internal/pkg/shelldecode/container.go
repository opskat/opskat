package shelldecode

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// optionArity 是容器工具一个选项的写法：带不带值。
type optionArity uint8

const (
	optionFlag  optionArity = iota + 1 // 不带值（--rm、-i）；--rm=false 这样写也认
	optionValue                        // 带值（-e FOO=1、-eFOO=1、--env=FOO=1）
)

// containerFlags 是 exec、run 的选项表（short 是单字母的短选项）。
type containerFlags struct {
	short map[byte]optionArity
	long  map[string]optionArity
}

// containerOptions 是 docker / podman / nerdctl 的 exec、run 的选项，按 Docker 29.5 的
// docker exec --help、docker run --help 整理；podman、nerdctl 沿用同名选项。表外的选项（各家独有的、
// 隐藏的、以后新增的）认不出值占几个词，遇到时不再找容器名，按分不清处理，漏列只会多问一次。
var containerOptions = map[string]containerFlags{
	"exec": {
		short: map[byte]optionArity{'d': optionFlag, 'i': optionFlag, 't': optionFlag,
			'e': optionValue, 'u': optionValue, 'w': optionValue},
		long: arities(
			[]string{"detach", "interactive", "privileged", "tty"},
			[]string{"detach-keys", "env", "env-file", "user", "workdir"}),
	},
	"run": {
		short: map[byte]optionArity{'d': optionFlag, 'i': optionFlag, 't': optionFlag, 'P': optionFlag, 'q': optionFlag,
			'a': optionValue, 'c': optionValue, 'e': optionValue, 'h': optionValue, 'l': optionValue, 'm': optionValue,
			'p': optionValue, 'u': optionValue, 'v': optionValue, 'w': optionValue},
		long: arities(
			[]string{"detach", "init", "interactive", "no-healthcheck", "oom-kill-disable", "privileged", "publish-all",
				"quiet", "read-only", "rm", "sig-proxy", "tty", "use-api-socket"},
			[]string{"add-host", "annotation", "attach", "blkio-weight", "blkio-weight-device", "cap-add", "cap-drop",
				"cgroup-parent", "cgroupns", "cidfile", "cpu-period", "cpu-quota", "cpu-rt-period", "cpu-rt-runtime",
				"cpu-shares", "cpus", "cpuset-cpus", "cpuset-mems", "detach-keys", "device", "device-cgroup-rule",
				"device-read-bps", "device-read-iops", "device-write-bps", "device-write-iops", "dns", "dns-option",
				"dns-search", "domainname", "entrypoint", "env", "env-file", "expose", "gpus", "group-add", "health-cmd",
				"health-interval", "health-retries", "health-start-interval", "health-start-period", "health-timeout",
				"hostname", "ip", "ip6", "ipc", "isolation", "label", "label-file", "link", "link-local-ip", "log-driver",
				"log-opt", "mac-address", "memory", "memory-reservation", "memory-swap", "memory-swappiness", "mount",
				"name", "network", "network-alias", "oom-score-adj", "pid", "pids-limit", "platform", "publish", "pull",
				"restart", "runtime", "security-opt", "shm-size", "stop-signal", "stop-timeout", "storage-opt", "sysctl",
				"tmpfs", "ulimit", "user", "userns", "uts", "volume", "volume-driver", "volumes-from", "workdir"}),
	},
}

func arities(flags, values []string) map[string]optionArity {
	m := make(map[string]optionArity, len(flags)+len(values))
	for _, f := range flags {
		m[f] = optionFlag
	}
	for _, v := range values {
		m[v] = optionValue
	}
	return m
}

// onlyContainerGroup 判断工具名与子命令之间只有 container 这个分组词（docker container exec）。
// 别的词（docker compose exec 的 compose、docker -H x exec 的全局选项）让选项表对不上，不认。
func onlyContainerGroup(words []*syntax.Word) bool {
	for _, w := range words {
		if v, ok := value(w); !ok || v != "container" {
			return false
		}
	}
	return true
}

// containerNameAt 是 args[from:]（exec、run 之后的词）里容器名、镜像名的下标：跳过 flags 里的选项与
// 它们的值，第一个位置参数就是它；docker 的 -- 只结束选项，之后的第一个词是它。遇到表外的选项、
// 选项之后少了值、单独作为值的词可能拆成几个词（-u $u），或认不出动态词里的选项时为 -1。
func containerNameAt(flags containerFlags, args []*syntax.Word, from int) int {
	for k := from; k < len(args); k++ {
		v, static := value(args[k])
		if !static {
			v = literalPrefix(args[k])
		}
		if static && v == "--" {
			if k+1 < len(args) {
				return k + 1
			}
			return -1
		}
		if !strings.HasPrefix(v, "-") || static && v == "-" {
			return k
		}
		var takesNext, ok bool
		if name, ok0 := strings.CutPrefix(v, "--"); ok0 {
			takesNext, ok = flags.longOption(name, static)
		} else {
			takesNext, ok = flags.shortOptions(v[1:], static)
		}
		if !ok {
			return -1
		}
		if takesNext {
			k++
			if k == len(args) {
				return -1
			}
			if _, ok := value(args[k]); !ok && !singleField(args[k]) && !isAssignment(literalPrefix(args[k])) {
				return -1 // -u $u 可能拆成几个词，之后的位置就对不上了；-e FOO=$X 照旧按一个值算
			}
		}
	}
	return -1
}

// longOption 认 --name、--name=value。static 为假时 name 只是词开头的字面部分，后面是动态的：
// 只有 = 已经出现（--name=$N）才认得出，值是动态的。takesNext 表示值是下一个词。
func (c containerFlags) longOption(name string, static bool) (takesNext, ok bool) {
	name, _, inline := strings.Cut(name, "=")
	if !static && !inline {
		return false, false
	}
	switch c.long[name] {
	case optionFlag:
		return false, true
	case optionValue:
		return !inline, true
	}
	return false, false
}

// shortOptions 认一组短选项（-it、-dit、-uroot、-eFOO=1）：带值的字母之后的部分是它的值，在最后时
// 值是下一个词。static 为假时 letters 只是词开头的字面部分：后面动态的部分要落在带值字母的值里（-e$X）。
func (c containerFlags) shortOptions(letters string, static bool) (takesNext, ok bool) {
	for i := 0; i < len(letters); i++ {
		switch c.short[letters[i]] {
		case optionFlag:
			continue
		case optionValue:
			return static && i == len(letters)-1, true
		}
		return false, false
	}
	return false, static && letters != ""
}

// singleField 判断词在 shell 里只会展开成一个词：没有通配与花括号展开，变量、命令替换都在双引号里，
// 也不是 "$@"、"${a[@]}" 这类在双引号里也会展开成几个词的写法。
func singleField(w *syntax.Word) bool {
	if hasBraces(w) || hasGlob(w) {
		return false
	}
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if pe, ok := inner.(*syntax.ParamExp); ok && (pe.Param.Value == "@" || pe.Index != nil || pe.Names != 0) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
