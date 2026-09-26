package adminapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in on Linux with Docker: this exercises the shipped nginx template against
// a real admin handler and disposable SQLite, not an intercepted browser route.
func TestNginxImageUploadLimits(t *testing.T) {
	if os.Getenv("ENGLISH_MCP_NGINX_TEST") != "1" {
		t.Skip("set ENGLISH_MCP_NGINX_TEST=1 with Docker available")
	}
	_, handler := testHandler(t)
	upstream := httptest.NewServer(handler)
	defer upstream.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	config, err := os.ReadFile("../../admin/nginx/default.conf.template")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(config), "${MCP_UPSTREAM}", upstream.URL)
	text = strings.Replace(text, "listen 80;", "listen "+address+";", 1)
	path := filepath.Join(t.TempDir(), "default.conf")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile("../../admin/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	image := ""
	for _, line := range strings.Split(string(dockerfile), "\n") {
		if strings.HasPrefix(line, "FROM nginx:") {
			image = strings.Fields(line)[1]
		}
	}
	if image == "" {
		t.Fatal("no pinned nginx runtime image found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "--network", "host",
		"--read-only", "--tmpfs", "/var/cache/nginx", "--tmpfs", "/var/run", "--tmpfs", "/tmp",
		"-v", path+":/etc/nginx/conf.d/default.conf:ro,Z", image)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("start nginx: %v %s", err, stderr.String())
	}
	id := strings.TrimSpace(string(output))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if t.Failed() {
			logs, _ := exec.CommandContext(cleanup, "docker", "logs", id).CombinedOutput()
			t.Logf("nginx: %s", logs)
		}
		if output, err := exec.CommandContext(cleanup, "docker", "rm", "-f", id).CombinedOutput(); err != nil {
			t.Errorf("remove nginx: %v %s", err, output)
		}
	}()
	client := &http.Client{Timeout: 10 * time.Second}
	base := "http://" + address + "/admin/api"
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := client.Get(base + "/session")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusUnauthorized {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx did not start: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	request := func(method, path, contentType string, body io.Reader) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", contentType)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	status, data := request("POST", "/vocabulary", "application/json", strings.NewReader(`{"term":"bank"}`))
	if status != 200 {
		t.Fatalf("create: %d %s", status, data)
	}
	var item vocabularyResponse
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	for _, size := range []int{2 << 20, 10 << 20, (10 << 20) + 1, 11 << 20} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("expectedRevision", fmt.Sprint(item.Revision)); err != nil {
			t.Fatal(err)
		}
		part, err := form.CreateFormFile("image", "image.png")
		if err != nil {
			t.Fatal(err)
		}
		// Padding after IEND keeps a decodable PNG while testing exact byte limits.
		image := make([]byte, size)
		copy(image, png)
		if _, err := part.Write(image); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		status, data := request("POST", "/vocabulary/"+item.ItemID+"/images", form.FormDataContentType(), &body)
		want := http.StatusOK
		if size > 10<<20 {
			want = http.StatusBadRequest
		}
		if size > maxImageRequestBytes {
			want = http.StatusRequestEntityTooLarge
		}
		if status != want {
			t.Fatalf("upload %d bytes: got %d want %d: %s", size, status, want, data)
		}
		if status == http.StatusOK {
			if err := json.Unmarshal(data, &item); err != nil {
				t.Fatal(err)
			}
		}
	}
	status, _ = request("POST", "/vocabulary", "application/json", strings.NewReader(strings.Repeat(" ", 2<<20)))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("ordinary request limit weakened: %d", status)
	}
}
