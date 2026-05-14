package codesearchtool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type languageServer struct {
	ID          string
	Title       string
	Command     []string
	Markers     []string
	Extensions  []string
	LanguageIDs map[string]string
}

type searchOptions struct {
	Action    string
	Query     string
	Path      string
	Language  string
	Line      int
	Character int
	Limit     int
}

type lspClient struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	mu        sync.Mutex
	nextID    int
	closeOnce sync.Once
}

type lspRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type lspResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspError       `json:"error,omitempty"`
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI       string   `json:"uri"`
	Range     lspRange `json:"range"`
	TargetURI string   `json:"targetUri"`
}

type symbolInformation struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	ContainerName string      `json:"containerName"`
	Location      lspLocation `json:"location"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
}

type lspResult struct {
	Language string
	Server   string
	Lines    []string
}

const (
	actionLanguages       = "languages"
	actionWorkspaceSymbol = "workspace_symbol"
	actionDocumentSymbols = "document_symbols"
	actionDefinition      = "definition"
	actionReferences      = "references"
	defaultLimit          = 100
	defaultTimeout        = 20 * time.Second
	defaultIdleTimeout    = 10 * time.Minute
	defaultSweepInterval  = 30 * time.Second
	defaultCloseTimeout   = 2 * time.Second
)

var languageServers = []languageServer{
	{
		ID:         "go",
		Title:      "Go",
		Command:    []string{"gopls"},
		Markers:    []string{"go.mod", "go.work"},
		Extensions: []string{".go"},
	},
	{
		ID:         "typescript",
		Title:      "TypeScript/JavaScript",
		Command:    []string{"typescript-language-server", "--stdio"},
		Markers:    []string{"package.json", "tsconfig.json", "jsconfig.json"},
		Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
	},
	{
		ID:         "python",
		Title:      "Python",
		Command:    []string{"pylsp"},
		Markers:    []string{"pyproject.toml", "setup.py", "requirements.txt"},
		Extensions: []string{".py"},
	},
	{
		ID:         "rust",
		Title:      "Rust",
		Command:    []string{"rust-analyzer"},
		Markers:    []string{"Cargo.toml"},
		Extensions: []string{".rs"},
	},
	{
		ID:         "cpp",
		Title:      "C/C++",
		Command:    []string{"clangd"},
		Markers:    []string{"compile_commands.json", "compile_flags.txt", "CMakeLists.txt", "Makefile"},
		Extensions: []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"},
		LanguageIDs: map[string]string{
			".c": "c", ".h": "c",
			".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hh": "cpp", ".hpp": "cpp", ".hxx": "cpp",
		},
	},
	{
		ID:         "java",
		Title:      "Java",
		Command:    []string{"jdtls"},
		Markers:    []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		Extensions: []string{".java"},
	},
	{
		ID:         "csharp",
		Title:      "C#",
		Command:    []string{"csharp-ls"},
		Markers:    []string{"*.sln", "*.csproj"},
		Extensions: []string{".cs"},
	},
	{
		ID:         "php",
		Title:      "PHP",
		Command:    []string{"intelephense", "--stdio"},
		Markers:    []string{"composer.json"},
		Extensions: []string{".php"},
	},
	{
		ID:         "ruby",
		Title:      "Ruby",
		Command:    []string{"ruby-lsp"},
		Markers:    []string{"Gemfile", ".ruby-version"},
		Extensions: []string{".rb"},
	},
	{
		ID:         "lua",
		Title:      "Lua",
		Command:    []string{"lua-language-server"},
		Markers:    []string{".luarc.json", ".luarc.jsonc", "stylua.toml"},
		Extensions: []string{".lua"},
	},
	{
		ID:         "bash",
		Title:      "Shell",
		Command:    []string{"bash-language-server", "start"},
		Markers:    []string{".shellcheckrc"},
		Extensions: []string{".sh", ".bash", ".zsh"},
		LanguageIDs: map[string]string{
			".sh": "shellscript", ".bash": "shellscript", ".zsh": "shellscript",
		},
	},
	{
		ID:         "json",
		Title:      "JSON",
		Command:    []string{"vscode-json-language-server", "--stdio"},
		Markers:    []string{},
		Extensions: []string{".json", ".jsonc"},
		LanguageIDs: map[string]string{
			".json": "json", ".jsonc": "jsonc",
		},
	},
	{
		ID:         "yaml",
		Title:      "YAML",
		Command:    []string{"yaml-language-server", "--stdio"},
		Markers:    []string{},
		Extensions: []string{".yaml", ".yml"},
		LanguageIDs: map[string]string{
			".yaml": "yaml", ".yml": "yaml",
		},
	},
	{
		ID:         "html",
		Title:      "HTML",
		Command:    []string{"vscode-html-language-server", "--stdio"},
		Markers:    []string{},
		Extensions: []string{".html", ".htm"},
		LanguageIDs: map[string]string{
			".html": "html", ".htm": "html",
		},
	},
	{
		ID:         "css",
		Title:      "CSS",
		Command:    []string{"vscode-css-language-server", "--stdio"},
		Markers:    []string{},
		Extensions: []string{".css", ".scss", ".sass", ".less"},
		LanguageIDs: map[string]string{
			".css": "css", ".scss": "scss", ".sass": "sass", ".less": "less",
		},
	},
	{
		ID:         "vue",
		Title:      "Vue",
		Command:    []string{"vue-language-server", "--stdio"},
		Markers:    []string{"vue.config.js", "vite.config.js", "vite.config.ts", "nuxt.config.js", "nuxt.config.ts"},
		Extensions: []string{".vue"},
	},
	{
		ID:         "svelte",
		Title:      "Svelte",
		Command:    []string{"svelteserver", "--stdio"},
		Markers:    []string{"svelte.config.js", "svelte.config.ts"},
		Extensions: []string{".svelte"},
	},
	{
		ID:         "kotlin",
		Title:      "Kotlin",
		Command:    []string{"kotlin-language-server"},
		Markers:    []string{"build.gradle.kts", "settings.gradle.kts"},
		Extensions: []string{".kt", ".kts"},
		LanguageIDs: map[string]string{
			".kt": "kotlin", ".kts": "kotlin",
		},
	},
	{
		ID:         "swift",
		Title:      "Swift",
		Command:    []string{"sourcekit-lsp"},
		Markers:    []string{"Package.swift"},
		Extensions: []string{".swift"},
	},
	{
		ID:         "dart",
		Title:      "Dart",
		Command:    []string{"dart", "language-server", "--protocol=lsp"},
		Markers:    []string{"pubspec.yaml"},
		Extensions: []string{".dart"},
	},
	{
		ID:         "terraform",
		Title:      "Terraform",
		Command:    []string{"terraform-ls", "serve"},
		Markers:    []string{".terraform.lock.hcl"},
		Extensions: []string{".tf", ".tfvars"},
		LanguageIDs: map[string]string{
			".tf": "terraform", ".tfvars": "terraform-vars",
		},
	},
	{
		ID:         "nix",
		Title:      "Nix",
		Command:    []string{"nil"},
		Markers:    []string{"flake.nix", "shell.nix"},
		Extensions: []string{".nix"},
	},
	{
		ID:         "zig",
		Title:      "Zig",
		Command:    []string{"zls"},
		Markers:    []string{"build.zig", "build.zig.zon"},
		Extensions: []string{".zig"},
	},
	{
		ID:         "haskell",
		Title:      "Haskell",
		Command:    []string{"haskell-language-server-wrapper", "--lsp"},
		Markers:    []string{"cabal.project", "stack.yaml", "package.yaml"},
		Extensions: []string{".hs", ".lhs"},
		LanguageIDs: map[string]string{
			".hs": "haskell", ".lhs": "literate haskell",
		},
	},
	{
		ID:         "ocaml",
		Title:      "OCaml",
		Command:    []string{"ocamllsp"},
		Markers:    []string{"dune-project", "dune"},
		Extensions: []string{".ml", ".mli"},
		LanguageIDs: map[string]string{
			".ml": "ocaml", ".mli": "ocaml.interface",
		},
	},
	{
		ID:         "scala",
		Title:      "Scala",
		Command:    []string{"metals"},
		Markers:    []string{"build.sbt", ".scala-build"},
		Extensions: []string{".scala", ".sc"},
	},
	{
		ID:         "clojure",
		Title:      "Clojure",
		Command:    []string{"clojure-lsp"},
		Markers:    []string{"deps.edn", "project.clj", "bb.edn"},
		Extensions: []string{".clj", ".cljs", ".cljc", ".edn"},
		LanguageIDs: map[string]string{
			".clj": "clojure", ".cljs": "clojure", ".cljc": "clojure", ".edn": "clojure",
		},
	},
}

var defaultLSPManager = newLSPManager(defaultIdleTimeout, defaultSweepInterval, defaultCloseTimeout)

// CloseLanguageServers shuts down all pooled language server subprocesses.
func CloseLanguageServers() {
	defaultLSPManager.close()
}

func parametersJSON() string {
	languages, _ := json.Marshal(languageIDs())
	return fmt.Sprintf(`{"type":"object","properties":{"action":{"type":"string","enum":["languages","workspace_symbol","document_symbols","definition","references"],"description":"LSP query to run. Defaults to workspace_symbol when query is provided."},"query":{"type":"string","description":"Workspace symbol query for action=workspace_symbol."},"path":{"type":"string","description":"Workspace file path for document_symbols, definition, or references, or optional scope hint for choosing a language server."},"language":{"type":"string","enum":%s,"description":"Optional language server to use. If omitted, all detected languages are queried for workspace_symbol, and path extension chooses for file actions."},"line":{"type":"integer","description":"1-indexed line for definition or references."},"character":{"type":"integer","description":"1-indexed UTF-16 character/column for definition or references."},"limit":{"type":"integer","description":"Maximum result lines to return. Defaults to 100."}},"additionalProperties":false}`, languages)
}

func languageIDs() []string {
	ids := make([]string, 0, len(languageServers))
	for _, server := range languageServers {
		ids = append(ids, server.ID)
	}
	return ids
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Search code",
		Description: "Query detected language servers for code navigation.",
		Usage:       "Launch and reuse well-known language server subprocesses for detected workspace languages, keeping them warm while idle for about 10 minutes. Use action=workspace_symbol with query to find symbols across the workspace. Use action=document_symbols with path to list symbols in one file. Use action=definition or action=references with path, line, and character for navigation at a source position. Use action=languages to see detected languages, available server commands, and missing language server commands. Prefer this tool for semantic code navigation; use grep for arbitrary text search and read to inspect returned files.",
		Parameters:  parametersJSON(),
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindCodeSearch }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	action := strings.TrimSpace(tools.FirstArg(args, "action", "mode"))
	query := strings.TrimSpace(tools.FirstArg(args, "query", "symbol", "name"))
	if action == "" {
		if query != "" {
			action = actionWorkspaceSymbol
		} else {
			action = actionLanguages
		}
	}
	if !validAction(action) {
		return nil, errors.New("action must be one of: languages, workspace_symbol, document_symbols, definition, references")
	}
	out := map[string]string{"action": action}
	if query != "" {
		out["query"] = query
	}
	if path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path")); path != "" {
		out["path"] = path
	}
	if language := strings.ToLower(strings.TrimSpace(tools.FirstArg(args, "language", "lang"))); language != "" {
		if _, ok := languageByID(language); !ok {
			return nil, fmt.Errorf("language must be one of: %s", strings.Join(languageIDs(), ", "))
		}
		out["language"] = language
	}
	if rawLine := strings.TrimSpace(tools.FirstArg(args, "line")); rawLine != "" {
		line, err := tools.ParseFlexibleInt(rawLine)
		if err != nil || line <= 0 {
			return nil, errors.New("line must be a positive integer")
		}
		out["line"] = strconv.Itoa(line)
	}
	if rawCharacter := strings.TrimSpace(tools.FirstArg(args, "character", "column", "col")); rawCharacter != "" {
		character, err := tools.ParseFlexibleInt(rawCharacter)
		if err != nil || character <= 0 {
			return nil, errors.New("character must be a positive integer")
		}
		out["character"] = strconv.Itoa(character)
	}
	if rawLimit := strings.TrimSpace(tools.FirstArg(args, "limit", "head_limit")); rawLimit != "" {
		limit, err := tools.ParseFlexibleInt(rawLimit)
		if err != nil || limit <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(limit)
	}
	if action == actionWorkspaceSymbol && query == "" {
		return nil, errors.New("query is required for workspace_symbol")
	}
	if (action == actionDocumentSymbols || action == actionDefinition || action == actionReferences) && out["path"] == "" {
		return nil, fmt.Errorf("path is required for %s", action)
	}
	if (action == actionDefinition || action == actionReferences) && (out["line"] == "" || out["character"] == "") {
		return nil, fmt.Errorf("line and character are required for %s", action)
	}
	return out, nil
}

func (tool) Preview(req tools.Request) string {
	if query := strings.TrimSpace(req.Args["query"]); query != "" {
		return query
	}
	if path := strings.TrimSpace(req.Args["path"]); path != "" {
		return strings.TrimSpace(req.Args["action"]) + " " + path
	}
	return strings.TrimSpace(req.Args["action"])
}

func (tool) Presentation(req tools.Request) tools.Presentation {
	preview := (tool{}).Preview(req)
	return tools.Presentation{Title: "Search code", Subtitle: preview, Preview: preview}
}

func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	options, err := optionsFromRequest(req)
	if err != nil {
		return tools.Result{}, err
	}
	rootAbs, _, err := tools.WorkspaceDir(runtime.Workdir, "")
	if err != nil {
		return tools.Result{}, err
	}
	detected, err := detectLanguages(rootAbs)
	if err != nil {
		return tools.Result{}, err
	}
	if options.Action == actionLanguages {
		return languagesResult(detected), nil
	}
	servers, err := selectedServers(rootAbs, detected, options)
	if err != nil {
		return tools.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	var lines []string
	var skipped []string
	for _, server := range servers {
		if _, err := exec.LookPath(server.Command[0]); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: command %q not found", server.ID, server.Command[0]))
			continue
		}
		result, err := defaultLSPManager.query(ctx, rootAbs, server, options)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", server.ID, err))
			continue
		}
		lines = append(lines, result.Lines...)
	}
	sort.Strings(lines)
	if options.Limit > 0 && len(lines) > options.Limit {
		lines = append(lines[:options.Limit], fmt.Sprintf("... truncated to first %d results ...", options.Limit))
	}
	if len(lines) == 0 {
		lines = append(lines, "No LSP results.")
	}
	if len(skipped) > 0 {
		lines = append(lines, "", "Skipped language servers:")
		lines = append(lines, skipped...)
	}
	if missing := missingDetectedServers(detected); len(missing) > 0 {
		lines = append(lines, "", "Missing language servers:")
		lines = append(lines, missing...)
	}
	body, truncated := tools.TruncateText(strings.Join(lines, "\n"), tools.DefaultToolOutputLimit)
	return tools.Result{
		Output: body,
		Meta: map[string]string{
			"action":    options.Action,
			"query":     options.Query,
			"path":      options.Path,
			"language":  options.Language,
			"truncated": tools.BoolString(truncated),
		},
	}, nil
}

func optionsFromRequest(req tools.Request) (searchOptions, error) {
	action := strings.TrimSpace(req.Args["action"])
	query := strings.TrimSpace(req.Args["query"])
	if action == "" {
		if query != "" {
			action = actionWorkspaceSymbol
		} else {
			action = actionLanguages
		}
	}
	if !validAction(action) {
		return searchOptions{}, errors.New("action must be one of: languages, workspace_symbol, document_symbols, definition, references")
	}
	options := searchOptions{
		Action:    action,
		Query:     query,
		Path:      strings.TrimSpace(req.Args["path"]),
		Language:  strings.TrimSpace(req.Args["language"]),
		Line:      parseStoredInt(req.Args["line"]),
		Character: parseStoredInt(req.Args["character"]),
		Limit:     defaultLimit,
	}
	if rawLimit := strings.TrimSpace(req.Args["limit"]); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 {
			return searchOptions{}, errors.New("limit must be a positive integer")
		}
		options.Limit = limit
	}
	return options, nil
}

func parseStoredInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func languagesResult(detected []languageServer) tools.Result {
	lines := []string{"Detected language servers:"}
	if len(detected) == 0 {
		lines = append(lines, "none")
	} else {
		for _, server := range detected {
			status := "available"
			if _, err := exec.LookPath(server.Command[0]); err != nil {
				status = "missing command"
			}
			lines = append(lines, fmt.Sprintf("%s: %s (%s) - %s", server.ID, server.Title, commandString(server), status))
		}
	}
	if missing := missingDetectedServers(detected); len(missing) > 0 {
		lines = append(lines, "", "Missing language servers:")
		lines = append(lines, missing...)
	}
	return tools.Result{
		Output: strings.Join(lines, "\n"),
		Meta:   map[string]string{"action": actionLanguages},
	}
}

func queryClient(ctx context.Context, client *lspClient, rootAbs string, server languageServer, options searchOptions) (lspResult, error) {
	switch options.Action {
	case actionWorkspaceSymbol:
		return client.workspaceSymbol(ctx, server, rootAbs, options.Query)
	case actionDocumentSymbols:
		return client.documentSymbols(ctx, server, rootAbs, options.Path)
	case actionDefinition:
		return client.definition(ctx, server, rootAbs, options.Path, options.Line, options.Character)
	case actionReferences:
		return client.references(ctx, server, rootAbs, options.Path, options.Line, options.Character)
	default:
		return lspResult{}, fmt.Errorf("unsupported action %q", options.Action)
	}
}

func startLSP(rootAbs string, server languageServer) (*lspClient, error) {
	cmd := exec.Command(server.Command[0], server.Command[1:]...)
	cmd.Dir = rootAbs
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &lspClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), nextID: 1}, nil
}

func (c *lspClient) initialize(ctx context.Context, rootAbs string) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   fileURI(rootAbs),
		"capabilities": map[string]any{
			"workspace": map[string]any{"symbol": map[string]any{}},
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{},
				"definition":     map[string]any{},
				"references":     map[string]any{},
			},
		},
		"workspaceFolders": []map[string]string{{
			"uri":  fileURI(rootAbs),
			"name": filepath.Base(rootAbs),
		}},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify(ctx, "initialized", map[string]any{})
}

func (c *lspClient) workspaceSymbol(ctx context.Context, server languageServer, rootAbs, query string) (lspResult, error) {
	raw, err := c.request(ctx, "workspace/symbol", map[string]string{"query": query})
	if err != nil {
		return lspResult{}, err
	}
	var symbols []symbolInformation
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return lspResult{}, err
	}
	lines := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		lines = append(lines, formatSymbol(server.ID, rootAbs, symbol))
	}
	return lspResult{Language: server.ID, Server: commandString(server), Lines: lines}, nil
}

func (c *lspClient) documentSymbols(ctx context.Context, server languageServer, rootAbs, path string) (lspResult, error) {
	abs, uri, text, err := textDocument(rootAbs, path)
	if err != nil {
		return lspResult{}, err
	}
	if err := c.didOpen(ctx, uri, languageIDForPath(server, path), text); err != nil {
		return lspResult{}, err
	}
	raw, err := c.request(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]string{"uri": uri}})
	if err != nil {
		return lspResult{}, err
	}
	lines, err := formatDocumentSymbols(server.ID, rootAbs, abs, raw)
	return lspResult{Language: server.ID, Server: commandString(server), Lines: lines}, err
}

func (c *lspClient) definition(ctx context.Context, server languageServer, rootAbs, path string, line, character int) (lspResult, error) {
	_, uri, text, err := textDocument(rootAbs, path)
	if err != nil {
		return lspResult{}, err
	}
	if err := c.didOpen(ctx, uri, languageIDForPath(server, path), text); err != nil {
		return lspResult{}, err
	}
	raw, err := c.request(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position": map[string]int{
			"line":      line - 1,
			"character": character - 1,
		},
	})
	if err != nil {
		return lspResult{}, err
	}
	lines, err := formatLocations(server.ID, rootAbs, raw)
	return lspResult{Language: server.ID, Server: commandString(server), Lines: lines}, err
}

func (c *lspClient) references(ctx context.Context, server languageServer, rootAbs, path string, line, character int) (lspResult, error) {
	_, uri, text, err := textDocument(rootAbs, path)
	if err != nil {
		return lspResult{}, err
	}
	if err := c.didOpen(ctx, uri, languageIDForPath(server, path), text); err != nil {
		return lspResult{}, err
	}
	raw, err := c.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position": map[string]int{
			"line":      line - 1,
			"character": character - 1,
		},
		"context": map[string]bool{"includeDeclaration": true},
	})
	if err != nil {
		return lspResult{}, err
	}
	lines, err := formatLocations(server.ID, rootAbs, raw)
	return lspResult{Language: server.ID, Server: commandString(server), Lines: lines}, err
}

func (c *lspClient) didOpen(ctx context.Context, uri, languageID, text string) error {
	return c.notify(ctx, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

func (c *lspClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	if err := writeLSP(c.stdin, lspRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	for {
		resp, err := c.readResponse(ctx)
		if err != nil {
			return nil, err
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, lspServerError{message: resp.Error.Message}
		}
		return resp.Result, nil
	}
}

func (c *lspClient) notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeLSP(c.stdin, lspRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *lspClient) readResponse(ctx context.Context) (lspResponse, error) {
	type readResult struct {
		response lspResponse
		err      error
	}
	out := make(chan readResult, 1)
	go func() {
		resp, err := readLSP(c.stdout)
		out <- readResult{response: resp, err: err}
	}()
	select {
	case result := <-out:
		return result.response, result.err
	case <-ctx.Done():
		c.kill()
		return lspResponse{}, ctx.Err()
	}
}

func (c *lspClient) closeWithTimeout(timeout time.Duration) {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		done := make(chan struct{})
		go func() {
			_, _ = c.request(ctx, "shutdown", nil)
			_ = c.notify(ctx, "exit", nil)
			_ = c.stdin.Close()
			_ = c.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			c.kill()
			_ = c.stdin.Close()
			select {
			case <-done:
			case <-time.After(timeout):
			}
		}
	})
}

func (c *lspClient) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func writeLSP(w io.Writer, msg lspRequest) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readLSP(r *bufio.Reader) (lspResponse, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return lspResponse{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return lspResponse{}, err
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return lspResponse{}, errors.New("missing LSP Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return lspResponse{}, err
	}
	var resp lspResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return lspResponse{}, err
	}
	return resp, nil
}

func detectLanguages(rootAbs string) ([]languageServer, error) {
	var detected []languageServer
	for _, server := range languageServers {
		ok, err := detectsLanguage(rootAbs, server)
		if err != nil {
			return nil, err
		}
		if ok {
			detected = append(detected, server)
		}
	}
	return detected, nil
}

func detectsLanguage(rootAbs string, server languageServer) (bool, error) {
	for _, marker := range server.Markers {
		if strings.ContainsAny(marker, "*?[") {
			matches, err := filepath.Glob(filepath.Join(rootAbs, marker))
			if err != nil {
				return false, err
			}
			if len(matches) > 0 {
				return true, nil
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(rootAbs, marker)); err == nil {
			return true, nil
		}
	}
	extensions := make(map[string]struct{}, len(server.Extensions))
	for _, ext := range server.Extensions {
		extensions[ext] = struct{}{}
	}
	found := false
	err := filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) && path != rootAbs {
				return filepath.SkipDir
			}
			return nil
		}
		_, found = extensions[filepath.Ext(path)]
		return nil
	})
	return found, err
}

func selectedServers(rootAbs string, detected []languageServer, options searchOptions) ([]languageServer, error) {
	if options.Language != "" {
		server, ok := languageByID(options.Language)
		if !ok {
			return nil, fmt.Errorf("unsupported language %q", options.Language)
		}
		return []languageServer{server}, nil
	}
	if options.Path != "" {
		if server, ok := languageForPath(options.Path); ok {
			return []languageServer{server}, nil
		}
		abs, _, err := tools.WorkspacePath(rootAbs, options.Path)
		if err == nil {
			if server, ok := languageForPath(abs); ok {
				return []languageServer{server}, nil
			}
		}
	}
	if len(detected) == 0 {
		return nil, errors.New("no supported workspace language detected")
	}
	return detected, nil
}

func missingDetectedServers(detected []languageServer) []string {
	var missing []string
	for _, server := range detected {
		if msg := missingCommand(server); msg != "" {
			missing = append(missing, msg)
		}
	}
	return missing
}

func languageIDForPath(server languageServer, path string) string {
	ext := filepath.Ext(path)
	if server.LanguageIDs != nil {
		if id := server.LanguageIDs[ext]; id != "" {
			return id
		}
	}
	return server.ID
}

func languageByID(id string) (languageServer, bool) {
	for _, server := range languageServers {
		if server.ID == id {
			return server, true
		}
	}
	return languageServer{}, false
}

func languageForPath(path string) (languageServer, bool) {
	ext := filepath.Ext(path)
	for _, server := range languageServers {
		for _, candidate := range server.Extensions {
			if ext == candidate {
				return server, true
			}
		}
	}
	return languageServer{}, false
}

func validAction(action string) bool {
	switch action {
	case actionLanguages, actionWorkspaceSymbol, actionDocumentSymbols, actionDefinition, actionReferences:
		return true
	default:
		return false
	}
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "target", ".venv", "__pycache__":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func textDocument(rootAbs, path string) (abs string, uri string, text string, err error) {
	abs, _, err = tools.WorkspacePath(rootAbs, path)
	if err != nil {
		return "", "", "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", "", err
	}
	return abs, fileURI(abs), string(data), nil
}

func fileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

func pathFromURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return uri
	}
	return filepath.FromSlash(parsed.Path)
}

func relPath(rootAbs, uri string) string {
	path := pathFromURI(uri)
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func formatSymbol(language, rootAbs string, symbol symbolInformation) string {
	location := symbol.Location
	uri := location.URI
	if uri == "" {
		uri = location.TargetURI
	}
	line := location.Range.Start.Line + 1
	character := location.Range.Start.Character + 1
	name := symbol.Name
	if symbol.ContainerName != "" {
		name = symbol.ContainerName + "." + name
	}
	return fmt.Sprintf("%s: %s:%d:%d: %s %s", language, relPath(rootAbs, uri), line, character, symbolKind(symbol.Kind), name)
}

func formatDocumentSymbols(language, rootAbs, abs string, raw json.RawMessage) ([]string, error) {
	var docSymbols []documentSymbol
	if err := json.Unmarshal(raw, &docSymbols); err == nil && len(docSymbols) > 0 {
		lines := make([]string, 0)
		var walk func([]documentSymbol, string)
		walk = func(symbols []documentSymbol, prefix string) {
			for _, symbol := range symbols {
				name := symbol.Name
				if prefix != "" {
					name = prefix + "." + name
				}
				lines = append(lines, fmt.Sprintf("%s: %s:%d:%d: %s %s", language, relPath(rootAbs, fileURI(abs)), symbol.SelectionRange.Start.Line+1, symbol.SelectionRange.Start.Character+1, symbolKind(symbol.Kind), name))
				walk(symbol.Children, name)
			}
		}
		walk(docSymbols, "")
		return lines, nil
	}
	var symbols []symbolInformation
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		lines = append(lines, formatSymbol(language, rootAbs, symbol))
	}
	return lines, nil
}

func formatLocations(language, rootAbs string, raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) || len(raw) == 0 {
		return nil, nil
	}
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && (single.URI != "" || single.TargetURI != "") {
		return []string{formatLocation(language, rootAbs, single)}, nil
	}
	var many []lspLocation
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(many))
	for _, location := range many {
		lines = append(lines, formatLocation(language, rootAbs, location))
	}
	return lines, nil
}

func formatLocation(language, rootAbs string, location lspLocation) string {
	uri := location.URI
	if uri == "" {
		uri = location.TargetURI
	}
	return fmt.Sprintf("%s: %s:%d:%d", language, relPath(rootAbs, uri), location.Range.Start.Line+1, location.Range.Start.Character+1)
}

func symbolKind(kind int) string {
	names := map[int]string{
		1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class", 6: "method", 7: "property", 8: "field",
		9: "constructor", 10: "enum", 11: "interface", 12: "function", 13: "variable", 14: "constant", 15: "string",
		16: "number", 17: "boolean", 18: "array", 19: "object", 20: "key", 21: "null", 22: "enumMember",
		23: "struct", 24: "event", 25: "operator", 26: "typeParameter",
	}
	if name, ok := names[kind]; ok {
		return name
	}
	return fmt.Sprintf("kind%d", kind)
}
