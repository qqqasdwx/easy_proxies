package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type socksFlag []string

func (f *socksFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *socksFlag) Set(value string) error {
	if !strings.Contains(value, ":") {
		return fmt.Errorf("socks listener must be name:port, got %q", value)
	}
	*f = append(*f, value)
	return nil
}

type eventLogger struct {
	path string
	mu   sync.Mutex
}

func (l *eventLogger) log(fields map[string]any) {
	fields["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(fields)
	if err != nil {
		log.Printf("marshal event: %v", err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("open log: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func main() {
	var socks socksFlag
	targetPort := flag.Int("target-port", 18080, "HTTP target listen port")
	logFile := flag.String("log-file", "", "JSONL event log path")
	flag.Var(&socks, "socks", "SOCKS listener in name:port form; may repeat")
	flag.Parse()

	if *logFile == "" {
		log.Fatal("--log-file is required")
	}
	if len(socks) == 0 {
		log.Fatal("at least one --socks listener is required")
	}
	logger := &eventLogger{path: *logFile}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go startTarget(ctx, *targetPort, logger)
	for _, spec := range socks {
		parts := strings.SplitN(spec, ":", 2)
		port, err := strconv.Atoi(parts[1])
		if err != nil || port <= 0 || port > 65535 {
			log.Fatalf("invalid socks port in %q", spec)
		}
		go startSOCKS(ctx, parts[0], port, logger)
	}

	select {}
}

func startTarget(ctx context.Context, port int, logger *eventLogger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.log(map[string]any{
			"kind":   "target_request",
			"path":   r.URL.RequestURI(),
			"method": r.Method,
		})
		if strings.HasPrefix(r.URL.Path, "/hold") {
			ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
			if ms <= 0 {
				ms = 1200
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		body := fmt.Sprintf("E2E OK %s\n", r.URL.RequestURI())
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	log.Printf("target listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("target server: %v", err)
	}
}

func startSOCKS(ctx context.Context, name string, port int, logger *eventLogger) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		log.Fatalf("socks %s listen: %v", name, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	log.Printf("socks %s listening on %s", name, ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Fatalf("socks %s accept: %v", name, err)
			}
		}
		go handleSOCKSConn(name, conn, logger)
	}
}

func handleSOCKSConn(name string, client net.Conn, logger *eventLogger) {
	defer client.Close()
	dst, err := socksHandshake(client)
	if err != nil {
		logger.log(map[string]any{"kind": "socks_error", "node": name, "error": err.Error()})
		return
	}
	server, err := net.DialTimeout("tcp", dst, 5*time.Second)
	if err != nil {
		_, _ = client.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		logger.log(map[string]any{"kind": "socks_error", "node": name, "dst": dst, "error": err.Error()})
		return
	}
	defer server.Close()
	_, _ = client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	firstLine, readErr := reader.ReadString('\n')
	_ = client.SetReadDeadline(time.Time{})
	if readErr == nil {
		method, path := parseHTTPRequestLine(firstLine)
		if method != "" {
			logger.log(map[string]any{
				"kind":   "socks_http_request",
				"node":   name,
				"dst":    dst,
				"method": method,
				"path":   path,
			})
		} else {
			logger.log(map[string]any{"kind": "socks_connect", "node": name, "dst": dst})
		}
		if _, err := io.WriteString(server, firstLine); err != nil {
			return
		}
	} else if readErr != nil && !isTimeout(readErr) {
		logger.log(map[string]any{"kind": "socks_error", "node": name, "dst": dst, "error": readErr.Error()})
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, reader)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server)
		done <- struct{}{}
	}()
	<-done
}

func socksHandshake(conn net.Conn) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", fmt.Errorf("read greeting: %w", err)
	}
	if header[0] != 0x05 {
		return "", fmt.Errorf("unsupported socks version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", fmt.Errorf("read methods: %w", err)
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", fmt.Errorf("write method selection: %w", err)
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return "", fmt.Errorf("read request: %w", err)
	}
	if req[0] != 0x05 || req[1] != 0x01 {
		return "", fmt.Errorf("unsupported socks request version=%d cmd=%d", req[0], req[1])
	}

	host, err := readSOCKSHost(conn, req[3])
	if err != nil {
		return "", err
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", fmt.Errorf("read dst port: %w", err)
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBuf)))), nil
}

func readSOCKSHost(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		buf := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", fmt.Errorf("read ipv4 host: %w", err)
		}
		return net.IP(buf).String(), nil
	case 0x03:
		lenBuf := []byte{0}
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", fmt.Errorf("read domain length: %w", err)
		}
		buf := make([]byte, int(lenBuf[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", fmt.Errorf("read domain: %w", err)
		}
		return string(buf), nil
	case 0x04:
		buf := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", fmt.Errorf("read ipv6 host: %w", err)
		}
		return net.IP(buf).String(), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}

func parseHTTPRequestLine(line string) (string, string) {
	parts := strings.Split(strings.TrimSpace(line), " ")
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "HTTP/") {
		return "", ""
	}
	path := parts[1]
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		if idx := strings.Index(path, "://"); idx >= 0 {
			rest := path[idx+3:]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				path = rest[slash:]
			}
		}
	}
	return parts[0], path
}

func isTimeout(err error) bool {
	var netErr net.Error
	return err != nil && strings.Contains(err.Error(), "i/o timeout") ||
		(err != nil && errorsAs(err, &netErr) && netErr.Timeout())
}

func errorsAs(err error, target any) bool {
	switch t := target.(type) {
	case *net.Error:
		if ne, ok := err.(net.Error); ok {
			*t = ne
			return true
		}
	}
	return false
}
