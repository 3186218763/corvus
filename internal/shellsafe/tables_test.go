package shellsafe

import (
	"strings"
	"testing"

	"corvus/internal/shellparse"
)

// readOnlyPositive maps every ReadOnlyCommands entry to a typical read-only
// invocation plus the base the classifier must report. The completeness guard
// below fails when this map drifts from the classification table, so a new
// table entry forces a positive case here.
var readOnlyPositive = map[string]struct {
	command string
	base    string
}{
	"cat":                  {"cat go.mod", "cat"},
	"head":                 {"head -n 5 README.md", "head"},
	"tail":                 {"tail -n 20 log.txt", "tail"},
	"less":                 {"less README.md", "less"},
	"more":                 {"more README.md", "more"},
	"ls":                   {"ls -la", "ls"},
	"find":                 {"find . -name '*.go' -print", "find"},
	"locate":               {"locate corvus", "locate"},
	"which":                {"which go", "which"},
	"whereis":              {"whereis ls", "whereis"},
	"type":                 {"type ls", "type"},
	"grep":                 {"grep -rn foo .", "grep"},
	"egrep":                {"egrep -i '^func' main.go", "egrep"},
	"fgrep":                {"fgrep -n TODO main.go", "fgrep"},
	"rg":                   {"rg pattern .", "rg"},
	"echo":                 {"echo hello", "echo"},
	"printf":               {`printf "%s\n" hello`, "printf"},
	"pwd":                  {"pwd", "pwd"},
	"cd":                   {"cd /tmp", "cd"},
	"whoami":               {"whoami", "whoami"},
	"id":                   {"id -u", "id"},
	"uname":                {"uname -a", "uname"},
	"hostname":             {"hostname", "hostname"},
	"date":                 {"date +%Y-%m-%d", "date"},
	"printenv":             {"printenv PATH", "printenv"},
	"wc":                   {"wc -l main.go", "wc"},
	"sort":                 {"sort file.txt", "sort"},
	"uniq":                 {"uniq -c file.txt", "uniq"},
	"cut":                  {"cut -d: -f1 /etc/passwd", "cut"},
	"tr":                   {"tr a-z A-Z", "tr"},
	"stat":                 {"stat main.go", "stat"},
	"file":                 {"file /bin/ls", "file"},
	"du":                   {"du -sh .", "du"},
	"df":                   {"df -h", "df"},
	"ps":                   {"ps aux", "ps"},
	"top":                  {"top -b -n 1", "top"},
	"htop":                 {"htop", "htop"},
	"diff":                 {"diff a.txt b.txt", "diff"},
	"cmp":                  {"cmp a.txt b.txt", "cmp"},
	"comm":                 {"comm a.txt b.txt", "comm"},
	"man":                  {"man ls", "man"},
	"info":                 {"info coreutils", "info"},
	"help":                 {"help cd", "help"},
	"true":                 {"true", "true"},
	"false":                {"false", "false"},
	"test":                 {"test -f main.go", "test"},
	"[":                    {"[ -f main.go ]", "["},
	"basename":             {"basename /a/b/c", "basename"},
	"dirname":              {"dirname /a/b/c", "dirname"},
	"realpath":             {"realpath main.go", "realpath"},
	"readlink":             {"readlink /usr/bin/go", "readlink"},
	"get-childitem":        {"Get-ChildItem -Path .", "get-childitem"},
	"get-content":          {"Get-Content -Path file.txt", "get-content"},
	"get-item":             {"Get-Item -Path file.txt", "get-item"},
	"get-location":         {"Get-Location", "get-location"},
	"get-process":          {"Get-Process -Name mongod", "get-process"},
	"get-command":          {"Get-Command -Name ls", "get-command"},
	"get-nettcpconnection": {"Get-NetTCPConnection -LocalPort 6379", "get-nettcpconnection"},
	"resolve-path":         {"Resolve-Path .", "resolve-path"},
	"select-string":        {"Select-String -Pattern foo file.txt", "select-string"},
	"measure-object":       {"Measure-Object -Property Length", "measure-object"},
	"compare-object":       {"Compare-Object -ReferenceObject a -DifferenceObject b", "compare-object"},
}

// readOnlyNegative maps every ReadOnlyCommands entry to a write-capable or
// smuggling variant that the classifier must reject. Negatives use shell
// syntax (redirects, substitution, pipelines, chaining) because the
// classification table itself deliberately ignores argument rigor — that is
// each consumer's job (see TestArgumentRigorIsConsumerResponsibility).
var readOnlyNegative = map[string]string{
	"cat":                  "cat a > out.txt",
	"head":                 "head -n5 x >> out.txt",
	"tail":                 "tail -f log 2>err.txt",
	"less":                 "less README < input.txt",
	"more":                 "more README | tee out.txt",
	"ls":                   "ls > listing.txt",
	"find":                 "find . -name x > hits.txt",
	"locate":               "locate x | xargs rm",
	"which":                "which go > out",
	"whereis":              "whereis ls && rm x",
	"type":                 "type ls; rm x",
	"grep":                 "grep foo bar > out.txt",
	"egrep":                "egrep foo bar | tee out",
	"fgrep":                "fgrep foo bar $(pwd)",
	"rg":                   "rg foo . `date`",
	"echo":                 "echo hi > out.txt",
	"printf":               "printf x > out.txt",
	"pwd":                  "pwd > out.txt",
	"cd":                   "cd /tmp && rm x",
	"whoami":               "whoami | tee out",
	"id":                   "id -u > out",
	"uname":                "uname -a >> out",
	"hostname":             "hostname > out",
	"date":                 "date +%s > out",
	"printenv":             "printenv > out",
	"wc":                   "wc -l f > out",
	"sort":                 "sort a > out.txt",
	"uniq":                 "uniq f > out",
	"cut":                  "cut -d: -f1 /etc/passwd > out",
	"tr":                   "tr a-z A-Z > out",
	"stat":                 "stat f > out",
	"file":                 "file /bin/ls > out",
	"du":                   "du -sh . | tee out",
	"df":                   "df -h > out",
	"ps":                   "ps aux > out",
	"top":                  "top -b -n 1 > out",
	"htop":                 "htop > out",
	"diff":                 "diff a b > patch.diff",
	"cmp":                  "cmp a b > out",
	"comm":                 "comm a b > out",
	"man":                  "man ls | tee out",
	"info":                 "info coreutils > out",
	"help":                 "help cd > out",
	"true":                 "true > out",
	"false":                "false > out",
	"test":                 "test -f x > out",
	"[":                    "[ -f x ] > out",
	"basename":             "basename /a > out",
	"dirname":              "dirname /a > out",
	"realpath":             "realpath x > out",
	"readlink":             "readlink x | tee out",
	"get-childitem":        "Get-ChildItem -Path . | Out-File out.txt",
	"get-content":          "Get-Content -Path x > out.txt",
	"get-item":             "Get-Item -Path x > out",
	"get-location":         "Get-Location > out",
	"get-process":          "Get-Process -Name mongod | Stop-Process",
	"get-command":          "Get-Command -Name ls > out",
	"get-nettcpconnection": "Get-NetTCPConnection -LocalPort 6379 > out",
	"resolve-path":         "Resolve-Path . > out",
	"select-string":        "Select-String -Pattern foo f > out",
	"measure-object":       "Measure-Object -Property Length > out",
	"compare-object":       "Compare-Object a b > out",
}

// prefixPositive maps every ReadOnlyPrefixes entry to a typical read-only
// invocation. Keyed by "base sub" so the completeness guard can check both
// levels of the table.
var prefixPositive = map[string]struct {
	command string
	base    string
	sub     string
}{
	"git log":               {"git log --oneline", "git", "log"},
	"git status":            {"git status", "git", "status"},
	"git diff":              {"git diff HEAD", "git", "diff"},
	"git show":              {"git show HEAD", "git", "show"},
	"git tag":               {"git tag", "git", "tag"},
	"git blame":             {"git blame main.go", "git", "blame"},
	"git grep":              {"git grep foo", "git", "grep"},
	"git ls-files":          {"git ls-files", "git", "ls-files"},
	"git ls-tree":           {"git ls-tree HEAD", "git", "ls-tree"},
	"git rev-parse":         {"git rev-parse HEAD", "git", "rev-parse"},
	"git rev-list":          {"git rev-list --count HEAD", "git", "rev-list"},
	"git describe":          {"git describe --tags", "git", "describe"},
	"git reflog":            {"git reflog", "git", "reflog"},
	"git shortlog":          {"git shortlog -n", "git", "shortlog"},
	"git whatchanged":       {"git whatchanged", "git", "whatchanged"},
	"git cherry":            {"git cherry -v", "git", "cherry"},
	"git cat-file":          {"git cat-file -p HEAD", "git", "cat-file"},
	"git for-each-ref":      {"git for-each-ref", "git", "for-each-ref"},
	"git name-rev":          {"git name-rev HEAD", "git", "name-rev"},
	"go vet":                {"go vet ./...", "go", "vet"},
	"go doc":                {"go doc fmt", "go", "doc"},
	"go list":               {"go list ./...", "go", "list"},
	"go version":            {"go version", "go", "version"},
	"go env":                {"go env GOPATH", "go", "env"},
	"npm ls":                {"npm ls", "npm", "ls"},
	"npm list":              {"npm list --depth=0", "npm", "list"},
	"npm view":              {"npm view react version", "npm", "view"},
	"npm info":              {"npm info react", "npm", "info"},
	"npm outdated":          {"npm outdated", "npm", "outdated"},
	"npm audit":             {"npm audit", "npm", "audit"},
	"cargo check":           {"cargo check", "cargo", "check"},
	"cargo doc":             {"cargo doc --no-deps", "cargo", "doc"},
	"cargo search":          {"cargo search serde", "cargo", "search"},
	"docker ps":             {"docker ps", "docker", "ps"},
	"docker images":         {"docker images", "docker", "images"},
	"docker inspect":        {"docker inspect x", "docker", "inspect"},
	"docker logs":           {"docker logs x", "docker", "logs"},
	"docker stats":          {"docker stats --no-stream", "docker", "stats"},
	"docker info":           {"docker info", "docker", "info"},
	"docker version":        {"docker version", "docker", "version"},
	"kubectl get":           {"kubectl get pods", "kubectl", "get"},
	"kubectl describe":      {"kubectl describe pod x", "kubectl", "describe"},
	"kubectl logs":          {"kubectl logs x", "kubectl", "logs"},
	"kubectl explain":       {"kubectl explain pods", "kubectl", "explain"},
	"kubectl api-resources": {"kubectl api-resources", "kubectl", "api-resources"},
	"kubectl api-versions":  {"kubectl api-versions", "kubectl", "api-versions"},
	"node -v":               {"node -v", "node", "-v"},
	"node --version":        {"node --version", "node", "--version"},
	"python --version":      {"python --version", "python", "--version"},
	// Note: -V folds to -v because lookup lowercases the subcommand; both are
	// read-only version/verbosity probes, so the collision is harmless.
	"python -v":         {"python -v", "python", "-v"},
	"python -V":         {"python -V", "python", "-v"},
	"python3 --version": {"python3 --version", "python3", "--version"},
	"python3 -v":        {"python3 -v", "python3", "-v"},
	"python3 -V":        {"python3 -V", "python3", "-v"},
}

// prefixNegative lists write-capable subcommands per base that must fail
// closed (they are deliberately absent from ReadOnlyPrefixes).
var prefixNegative = map[string][]string{
	"git": {
		"git push", "git push --force", "git commit -m x", "git checkout main",
		"git reset --hard", "git branch -d feature", "git branch feature",
		"git remote add o url", "git clone url", "git fetch", "git pull",
		"git merge x", "git rebase x", "git stash",
		"git config user.name x", "git add .", "git rm x",
		"git mv a b", "git clean -f", "git apply patch.diff",
	},
	"go": {
		"go build ./...", "go test ./...", "go run main.go", "go install x",
		"go get x", "go mod tidy", "go mod init x", "go generate", "go work sync",
	},
	"npm": {
		"npm install", "npm i -g x", "npm run build", "npm build",
		"npm publish", "npm uninstall x", "npm update", "npm init", "npm pack",
	},
	"cargo": {
		"cargo build", "cargo run", "cargo install x", "cargo init",
		"cargo new x", "cargo test", "cargo publish",
	},
	"docker": {
		"docker rm x", "docker rmi x", "docker build .", "docker run x",
		"docker stop x", "docker pull x", "docker push x", "docker exec x y",
		"docker commit x y", "docker tag x y",
	},
	"kubectl": {
		"kubectl apply -f x.yaml", "kubectl delete pod x", "kubectl create -f x",
		"kubectl edit deploy x", "kubectl scale deploy x --replicas=3",
		"kubectl exec pod x -- rm -rf /", "kubectl label pod x a=b",
		"kubectl annotate pod x a=b", "kubectl rollout restart deploy x",
	},
	"node":    {"node -e 'x'", "node --eval x", "node app.js", "node -p x"},
	"python":  {"python -c 'x'", "python script.py", "python -m http.server"},
	"python3": {"python3 -c 'x'", "python3 script.py", "python3 -m http.server"},
}

// TestReadOnlyClassificationCoverageIsComplete guards the positive/negative
// maps against table drift: every entry in the exported classification tables
// must have at least one positive and one negative case.
func TestReadOnlyClassificationCoverageIsComplete(t *testing.T) {
	for cmd := range ReadOnlyCommands {
		if _, ok := readOnlyPositive[cmd]; !ok {
			t.Errorf("ReadOnlyCommands[%q] has no positive case", cmd)
		}
		if _, ok := readOnlyNegative[cmd]; !ok {
			t.Errorf("ReadOnlyCommands[%q] has no negative case", cmd)
		}
	}
	for base, subs := range ReadOnlyPrefixes {
		for sub := range subs {
			key := base + " " + sub
			if _, ok := prefixPositive[key]; !ok {
				t.Errorf("ReadOnlyPrefixes[%q][%q] has no positive case", base, sub)
			}
		}
		if _, ok := prefixNegative[base]; !ok || len(prefixNegative[base]) == 0 {
			t.Errorf("ReadOnlyPrefixes[%q] has no negative subcommand cases", base)
		}
	}
}

// TestEveryReadOnlyCommandPositive exercises every positive case and asserts
// the returned base matches the table entry.
func TestEveryReadOnlyCommandPositive(t *testing.T) {
	for cmd, tc := range readOnlyPositive {
		t.Run(cmd, func(t *testing.T) {
			base, sub, ok := CommandIsReadOnly(tc.command)
			if !ok {
				t.Fatalf("CommandIsReadOnly(%q) = false, want true", tc.command)
			}
			if base != tc.base {
				t.Errorf("base = %q, want %q", base, tc.base)
			}
			if _, prefixed := ReadOnlyPrefixes[tc.base]; !prefixed && sub != "" {
				t.Errorf("sub = %q for single-word command %q, want empty", sub, tc.command)
			}
		})
	}
}

// TestEveryReadOnlyPrefixPositive exercises every prefix positive case and
// asserts the returned subcommand matches the table entry.
func TestEveryReadOnlyPrefixPositive(t *testing.T) {
	for key, tc := range prefixPositive {
		t.Run(key, func(t *testing.T) {
			base, sub, ok := CommandIsReadOnly(tc.command)
			if !ok {
				t.Fatalf("CommandIsReadOnly(%q) = false, want true", tc.command)
			}
			if base != tc.base || sub != tc.sub {
				t.Fatalf("CommandIsReadOnly(%q) = (%q, %q), want (%q, %q)",
					tc.command, base, sub, tc.base, tc.sub)
			}
		})
	}
}

// TestEveryReadOnlyCommandNegative exercises the per-command negative
// variants; each must be rejected.
func TestEveryReadOnlyCommandNegative(t *testing.T) {
	for cmd, neg := range readOnlyNegative {
		t.Run(cmd, func(t *testing.T) {
			if _, _, ok := CommandIsReadOnly(neg); ok {
				t.Fatalf("CommandIsReadOnly(%q) = true, want false", neg)
			}
			if !shellparse.ContainsShellSyntax(neg) {
				t.Errorf("shellparse.ContainsShellSyntax(%q) = false, want true", neg)
			}
		})
	}
}

// TestEveryPrefixBaseNegative exercises the write-capable subcommand variants
// per base; each must fail closed.
func TestEveryPrefixBaseNegative(t *testing.T) {
	for _, negs := range prefixNegative {
		for _, neg := range negs {
			t.Run(neg, func(t *testing.T) {
				if gotBase, _, ok := CommandIsReadOnly(neg); ok {
					t.Fatalf("CommandIsReadOnly(%q) = (%q, true), want false", neg, gotBase)
				}
			})
		}
	}
}

// TestArgumentRigorIsConsumerResponsibility locks the documented semantics of
// CommandIsReadOnly: it classifies base/subcommand membership only and ignores
// argument rigor. Write-capable variants of read-only commands therefore still
// classify as read-only here; the permission layer is the consumer that must
// reject them (bash_readonly.go hasUnsafeReadOnlyArgs/hasUnsafePrefixArgs).
// If any of these become false, the shared-table contract changed and the
// permission layer's argument checks must be re-audited.
func TestArgumentRigorIsConsumerResponsibility(t *testing.T) {
	writeVariants := []string{
		"sort -o out.txt",
		"go env -w GOPATH=/x",
		"find . -name x -exec rm {} \\;",
		"git tag -d v1",
		"git log -o out.txt",
		"git diff --output=out.patch",
		"git show --output out.txt",
	}
	for _, c := range writeVariants {
		if _, _, ok := CommandIsReadOnly(c); !ok {
			t.Errorf("CommandIsReadOnly(%q) = false, want true (table membership only)", c)
		}
	}
}

// TestContainsShellSyntaxSmugglingPatterns applies each write-smuggling shell
// operator to several read-only base commands; all must be flagged.
func TestContainsShellSyntaxSmugglingPatterns(t *testing.T) {
	bases := []string{"cat go.mod", "grep foo bar", "ls -la", "echo hi", "sort x", "find . -name x"}
	patterns := []string{
		" > out.txt", " >> out.txt", " 2>err.txt", " &>out.txt", " &>>out.txt",
		" < in.txt", " <<< word", " | tee out.txt", " $(rm -rf x)", " `rm -rf x`",
		" && rm -rf x", " || rm x", "; rm x", " &", " > >(tee out.txt)",
	}
	for _, base := range bases {
		for _, p := range patterns {
			cmd := base + p
			t.Run(cmd, func(t *testing.T) {
				if !shellparse.ContainsShellSyntax(cmd) {
					t.Errorf("shellparse.ContainsShellSyntax(%q) = false, want true", cmd)
				}
				if _, _, ok := CommandIsReadOnly(cmd); ok {
					t.Errorf("CommandIsReadOnly(%q) = true, want false", cmd)
				}
			})
		}
	}
}

// TestContainsShellSyntaxQuotedOperatorsAreInert ensures quoting/escaping keeps
// operators as literal argument text, so they cannot be mistaken for syntax.
func TestContainsShellSyntaxQuotedOperatorsAreInert(t *testing.T) {
	inert := []string{
		`echo "a > b"`,
		`echo 'x | y'`,
		`grep 'a|b' file`,
		`echo "a && b"`,
		`echo \$HOME`,
		`git log "2>/dev/null"`,
		`git log 2\>/dev/null`,
		`find . -name '*.go' -print`,
	}
	for _, c := range inert {
		if shellparse.ContainsShellSyntax(c) {
			t.Errorf("shellparse.ContainsShellSyntax(%q) = true, want false", c)
		}
	}
}

// TestCommandIsReadOnlyEdgeCases covers empty/unknown/case handling.
func TestCommandIsReadOnlyEdgeCases(t *testing.T) {
	tests := []struct {
		command  string
		wantOK   bool
		wantBase string
		wantSub  string
	}{
		{command: ""},
		{command: "   "},
		{command: "cat", wantOK: true, wantBase: "cat"},
		{command: "CAT go.mod", wantOK: true, wantBase: "cat"},
		{command: "GIT STATUS", wantOK: true, wantBase: "git", wantSub: "status"},
		{command: "Git Log --oneline", wantOK: true, wantBase: "git", wantSub: "log"},
		{command: "git"},
		{command: "git --version"},
		{command: "frobnicate --all"},
		{command: "tee out.txt"},
		{command: "xargs rm"},
		{command: "touch x"},
		{command: "sed -i s/a/b/ f"},
		{command: "awk '{print $1}' f"},
		{command: "curl -o out http://x"},
		{command: "wget -O out http://x"},
		{command: "echo $HOME"},
		{command: "echo $(date)"},
		{command: "echo `date`"},
		{command: "sleep 1 &"},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.command, "/", "_"), func(t *testing.T) {
			base, sub, ok := CommandIsReadOnly(tt.command)
			if ok != tt.wantOK {
				t.Fatalf("CommandIsReadOnly(%q) ok = %v, want %v", tt.command, ok, tt.wantOK)
			}
			if base != tt.wantBase || sub != tt.wantSub {
				t.Errorf("CommandIsReadOnly(%q) = (%q, %q), want (%q, %q)",
					tt.command, base, sub, tt.wantBase, tt.wantSub)
			}
		})
	}
}

// TestCommandIsWorkspaceNonMutating covers the network-probe separation and
// the subset relation between read-only and workspace-non-mutating.
func TestCommandIsWorkspaceNonMutating(t *testing.T) {
	tests := []struct {
		command string
		wantOK  bool
	}{
		{command: `Test-NetConnection -ComputerName example.com -Port 443`, wantOK: true},
		{command: `Test-NetConnection example.com; Set-Content out.txt bad`},
		{command: `Set-Content out.txt bad`},
		{command: `Remove-Item x`},
		{command: `rm -rf /`},
		{command: ""},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			_, _, ok := CommandIsWorkspaceNonMutating(tt.command)
			if ok != tt.wantOK {
				t.Fatalf("CommandIsWorkspaceNonMutating(%q) = %v, want %v", tt.command, ok, tt.wantOK)
			}
		})
	}
	// Permission-safe readers are a subset of workspace-non-mutating: every
	// positive read-only case must also classify as workspace-non-mutating.
	for _, tc := range readOnlyPositive {
		if _, _, ok := CommandIsWorkspaceNonMutating(tc.command); !ok {
			t.Errorf("CommandIsWorkspaceNonMutating(%q) = false, want true (read-only subset)", tc.command)
		}
	}
	for _, tc := range prefixPositive {
		if _, _, ok := CommandIsWorkspaceNonMutating(tc.command); !ok {
			t.Errorf("CommandIsWorkspaceNonMutating(%q) = false, want true (read-only subset)", tc.command)
		}
	}
	// A network probe must never classify as a permission reader.
	if _, _, ok := CommandIsReadOnly(`Test-NetConnection -ComputerName example.com -Port 443`); ok {
		t.Error("Test-NetConnection must not classify as read-only")
	}
}
