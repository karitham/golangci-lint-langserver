package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sourcegraph/jsonrpc2"
)

func NewHandler(logger logger, noLinterName bool) jsonrpc2.Handler {
	handler := &langHandler{
		logger:       logger,
		request:      make(chan DocumentURI),
		noLinterName: noLinterName,
	}
	go handler.linter()

	return jsonrpc2.HandlerWithError(handler.handle)
}

type langHandler struct {
	logger       logger
	conn         *jsonrpc2.Conn
	request      chan DocumentURI
	command      []string
	noLinterName bool

	rootURI string
	rootDir string
}

// As defined in the `golangci-lint` source code:
// https://github.com/golangci/golangci-lint/blob/main/pkg/exitcodes/exitcodes.go#L24
const GoNoFilesExitCode = 5

func (h *langHandler) errToDiagnostics(err error) []Diagnostic {
	var message string
	switch e := err.(type) {
	case *exec.ExitError:
		if e.ExitCode() == GoNoFilesExitCode {
			return []Diagnostic{}
		}
		message = string(e.Stderr)
	default:
		h.logger.DebugJSON("golangci-lint-langserver: errToDiagnostics message", message)
		message = e.Error()
	}
	return []Diagnostic{
		{Severity: DSError, Message: message},
	}
}

func (h *langHandler) lint(uri DocumentURI) ([]Diagnostic, error) {
	diagnostics := make([]Diagnostic, 0)

	path := uriToPath(string(uri))
	dir, _ := filepath.Split(path)

	args := make([]string, 0, len(h.command))
	args = append(args, h.command[1:]...)
	args = append(args, dir)
	cmd := exec.Command(h.command[0], args...)

	if strings.HasPrefix(path, h.rootDir) {
		cmd.Dir = h.rootDir
	} else {
		cmd.Dir = dir
	}

	h.logger.DebugJSON("golangci-lint-langserver: golingci-lint cmd", cmd.String())

	b, err := cmd.Output()
	if err == nil {
		return diagnostics, nil
	} else if len(b) == 0 {
		// golangci-lint would output critical error to stderr rather than stdout
		// https://github.com/nametake/golangci-lint-langserver/issues/24
		return h.errToDiagnostics(err), nil
	}

	var result GolangCILintResult
	if err := json.Unmarshal(b, &result); err != nil {
		return h.errToDiagnostics(err), nil
	}

	h.logger.DebugJSON("golangci-lint-langserver: result:", result)

	for _, issue := range result.Issues {
		if path != issue.Pos.Filename {
			continue
		}

		diagnostics = append(diagnostics, Diagnostic{
			Range: Range{
				Start: Position{
					Line:      max(issue.Pos.Line-1, 0),
					Character: max(issue.Pos.Column-1, 0),
				},
				End: Position{
					Line:      max(issue.Pos.Line-1, 0),
					Character: max(issue.Pos.Column-1, 0),
				},
			},
			Severity: issue.DiagSeverity(),
			Source:   &issue.FromLinter,
			Message:  h.diagnosticMessage(&issue),
		})
	}

	return diagnostics, nil
}

func (h *langHandler) diagnosticMessage(issue *Issue) string {
	if h.noLinterName {
		return issue.Text
	}

	return fmt.Sprintf("%s: %s", issue.FromLinter, issue.Text)
}

func (h *langHandler) linter() {
	for {
		uri, ok := <-h.request
		if !ok {
			break
		}

		diagnostics, err := h.lint(uri)
		if err != nil {
			h.logger.Printf("%s\n", err)

			continue
		}

		if err := h.conn.Notify(
			context.Background(),
			"textDocument/publishDiagnostics",
			&PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: diagnostics,
			}); err != nil {
			h.logger.Printf("%s\n", err)
		}
	}
}

func (h *langHandler) handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	h.logger.DebugJSON("golangci-lint-langserver: request:", req)

	switch req.Method {
	case "initialize":
		return h.handleInitialize(ctx, conn, req)
	case "initialized":
		return
	case "shutdown":
		return h.handleShutdown(ctx, conn, req)
	case "textDocument/didOpen":
		return h.handleTextDocumentDidOpen(ctx, conn, req)
	case "textDocument/didClose":
		return h.handleTextDocumentDidClose(ctx, conn, req)
	case "textDocument/didChange":
		return h.handleTextDocumentDidChange(ctx, conn, req)
	case "textDocument/didSave":
		return h.handleTextDocumentDidSave(ctx, conn, req)
	case "workspace/didChangeConfiguration":
		return h.handlerWorkspaceDidChangeConfiguration(ctx, conn, req)
	}

	return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: fmt.Sprintf("method not supported: %s", req.Method)}
}

func (h *langHandler) handleInitialize(_ context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	var params InitializeParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	h.rootURI = params.RootURI
	h.rootDir = uriToPath(params.RootURI)
	h.conn = conn
	h.command = params.InitializationOptions.Command

	return InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: TextDocumentSyncOptions{
				Change:    TDSKNone,
				OpenClose: true,
				Save:      true,
			},
		},
	}, nil
}

func (h *langHandler) handleShutdown(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request) (result any, err error) {
	close(h.request)

	return nil, nil
}

func (h *langHandler) handleTextDocumentDidOpen(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	var params DidOpenTextDocumentParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	h.request <- params.TextDocument.URI

	return nil, nil
}

func (h *langHandler) handleTextDocumentDidClose(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request) (result any, err error) {
	return nil, nil
}

func (h *langHandler) handleTextDocumentDidChange(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request) (result any, err error) {
	return nil, nil
}

func (h *langHandler) handleTextDocumentDidSave(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	var params DidSaveTextDocumentParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	h.request <- params.TextDocument.URI

	return nil, nil
}

func (h *langHandler) handlerWorkspaceDidChangeConfiguration(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request) (result any, err error) {
	return nil, nil
}
