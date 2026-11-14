// The MCP stdio transport: Serve and its newline-delimited read/write loop.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"time"
)

const maxStdioCalls = 8
const maxStdioLine = 4 * 1024 * 1024
const stdioShutdownTimeout = time.Second

// Serve reads and writes newline-delimited JSON-RPC messages as required by the
// MCP stdio transport. Tool responses may arrive in any order, matched by ID.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.API == nil || s.Credential == "" {
		return errors.New("mcp: API and credential are required")
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	type input struct {
		line     []byte
		tooLarge bool
		err      error
	}
	scanLines := make(chan input)
	lines := scanLines
	go func() {
		rd := bufio.NewReaderSize(in, 64*1024)
		for {
			line, tooLarge, err := readLine(rd, maxStdioLine)
			if tooLarge || len(bytes.TrimSpace(line)) > 0 {
				select {
				case scanLines <- input{line: line, tooLarge: tooLarge}:
				case <-runCtx.Done():
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				select {
				case scanLines <- input{err: err}:
				case <-runCtx.Done():
				}
				return
			}
		}
	}()
	defer func() {
		// A closable reader lets shutdown interrupt an outstanding Scan.
		if lines != nil {
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()
	type call struct {
		ctx    context.Context
		cancel context.CancelFunc
	}
	type completion struct {
		key string
		res response
	}
	pending := make(map[string]call)
	completed := make(chan completion, maxStdioCalls)
	defer func() {
		for _, c := range pending {
			c.cancel()
		}
	}()
	// Only this loop writes responses or changes connection state.
	enc := json.NewEncoder(out)
	initialized := false
	parentDone := ctx.Done()
	var shutdown <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	startShutdown := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(stdioShutdownTimeout)
		shutdown = timer.C
	}
	var serveErr error
	for {
		if lines == nil && len(pending) == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return serveErr
		}
		select {
		case <-parentDone:
			serveErr = ctx.Err()
			parentDone = nil
			lines = nil
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
			stop()
			startShutdown()
		case <-shutdown:
			if runCtx.Err() != nil {
				return errors.Join(serveErr, errors.New("mcp: tool calls did not stop before shutdown deadline"))
			}
			cancelled := 0
			for _, c := range pending {
				if c.ctx.Err() == nil {
					cancelled++
				}
			}
			if cancelled > 0 {
				serveErr = fmt.Errorf("mcp: EOF shutdown cancelled %d tool calls", cancelled)
			}
			stop()
			startShutdown()
		case done := <-completed:
			c := pending[done.key]
			delete(pending, done.key)
			cancelled := c.ctx.Err() != nil
			c.cancel()
			if !cancelled {
				if err := enc.Encode(done.res); err != nil {
					return err
				}
			}
		case next := <-lines:
			if next.tooLarge {
				// The id is unknowable, so the error goes to id null.
				if err := enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{-32600, "request too large"}}); err != nil {
					return err
				}
				continue
			}
			if next.line == nil {
				lines = nil
				serveErr = next.err
				if next.err != nil {
					stop()
				}
				startShutdown()
				continue
			}
			req, reject := readRequest(next.line)
			if reject != nil {
				if err := enc.Encode(reject); err != nil {
					return err
				}
				continue
			}
			if req.Method == "" {
				continue // a client response; nothing was asked of this server
			}
			if len(req.ID) == 0 {
				if req.Method == "notifications/cancelled" {
					var params struct {
						RequestID json.RawMessage `json:"requestId"`
						Reason    *string         `json:"reason"`
					}
					if json.Unmarshal(req.Params, &params) == nil {
						if c, ok := pending[stdioRequestKey(params.RequestID)]; ok {
							c.cancel()
						}
					}
				}
				continue
			}
			res := response{JSONRPC: "2.0", ID: req.ID}
			switch {
			case req.Method == "initialize" && initialized:
				res.Error = &rpcError{-32600, "already initialized"}
			case req.Method != "initialize" && req.Method != "ping" && !initialized:
				res.Error = &rpcError{-32002, "server not initialized"}
			case req.Method == "tools/call":
				key := stdioRequestKey(req.ID)
				if key == "" {
					res.Error = &rpcError{-32600, "invalid request"}
				} else if _, exists := pending[key]; exists {
					res.Error = &rpcError{-32600, "request ID already in flight"}
				} else if len(pending) >= maxStdioCalls {
					res.Error = &rpcError{-32000, "too many tool calls in flight"}
				} else {
					callCtx, cancel := context.WithCancel(runCtx)
					pending[key] = call{ctx: callCtx, cancel: cancel}
					go func() {
						res := response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32603, "internal error"}}
						defer func() {
							if recover() != nil {
								// Panic values can contain credentials or message bodies.
								log.Print("mcp: recovered panic in tool call")
							}
							completed <- completion{key: key, res: res}
						}()
						res = s.handle(callCtx, req)
					}()
					continue
				}
			default:
				res = s.handle(runCtx, req)
				if req.Method == "initialize" && res.Error == nil {
					initialized = true
				}
			}
			if err := enc.Encode(res); err != nil {
				return err
			}
		}
	}
}

// readLine returns the next line without its terminator. A line longer than
// limit is discarded through its newline, never buffered, and reported as
// tooLarge so one oversized message cannot end the connection.
func readLine(rd *bufio.Reader, limit int) (line []byte, tooLarge bool, err error) {
	for {
		chunk, e := rd.ReadSlice('\n')
		if !tooLarge {
			line = append(line, chunk...)
			if tooLarge = len(line) > limit+2; tooLarge {
				line = nil
			}
		}
		if e != bufio.ErrBufferFull {
			err = e
			break
		}
	}
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
	if len(line) > limit {
		return nil, true, err
	}
	return line, tooLarge, err
}
