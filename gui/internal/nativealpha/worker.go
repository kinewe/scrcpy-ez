package nativealpha

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RunIfRequested is an early, non-UI mode; it never starts device discovery.
func RunIfRequested(args []string) bool {
	if len(args) == 0 || args[0] != "--native-alpha-worker" {
		return false
	}
	if len(args) != 6 {
		fmt.Println("NATIVE_ALPHA_WORKER invalid arguments")
		return true
	}
	executable, _ := os.Executable()
	path := ProfilePath(executable)
	candidate := Prioritize(Candidates(path, args[4]), "")
	encode := func(records []Record) []byte {
		value := map[string]any{"recipes": PortableTemplates(records), "hostNow": time.Now().UnixMilli()}

		b, _ := json.Marshal(value)
		return b
	}
	payload := encode(candidate)
	// Respect Windows command-line limits; the most recent entries have priority.
	for len(payload) > 12000 && len(candidate) > 0 {
		candidate = candidate[:len(candidate)-1]
		payload = encode(candidate)
	}
	argv := []string{"-s", args[2], "shell", args[3], "app_process", "/", "com.genymobile.scrcpy.NativeAlphaProbe", args[4], args[5], base64.RawURLEncoding.EncodeToString(payload)}
	cmd := exec.Command(args[1], argv...)
	hide(cmd)
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("NATIVE_ALPHA_WORKER", err)
		return true
	}
	cmd.Stderr = cmd.Stdout
	if err = cmd.Start(); err != nil {
		fmt.Println("NATIVE_ALPHA_WORKER", err)
		return true
	}
	// Disk writes happen on a bounded latest-result queue, outside the stream reader.
	queue := make(chan Record, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for r := range queue {
			if err := WriteRecord(path, r); err != nil {
				fmt.Println("NATIVE_ALPHA_PROFILE_SAVE failed:", err)
			} else {
				fmt.Printf("NATIVE_ALPHA_PROFILE_SAVED package=%s status=%s cache=%s\n", r.Package, r.Status, r.Cache)
			}
		}
	}()
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "NATIVE_ALPHA_RESULT ") {
			var r Record
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "NATIVE_ALPHA_RESULT ")), &r) != nil || r.Package != args[4] {
				fmt.Println("NATIVE_ALPHA_PROFILE rejected invalid result")
				continue
			}
			// Timestamp belongs to this host; device wall clocks can differ.
			now := time.Now().UnixMilli()
			if r.RetryAfter > r.ObservedAt {
				r.RetryAfter = now + (r.RetryAfter - r.ObservedAt)
			}
			r.ObservedAt = now
			if Validate(r) != nil {
				fmt.Println("NATIVE_ALPHA_PROFILE rejected invalid result")
				continue
			}
			encoded, _ := json.Marshal(r)
			if os.Getenv("SCEZ_NATIVE_ALPHA_PROFILE_OWNER") == "gui" {
				fmt.Println("SCRCPY_EZ_NATIVE_ALPHA_PROFILE " + base64.RawURLEncoding.EncodeToString(encoded))
			} else {
				select {
				case queue <- r:
				default:
					fmt.Println("NATIVE_ALPHA_PROFILE_SAVE queue full")
				}
			}
		} else {
			fmt.Println(line)
		}
	}
	if scanner.Err() != nil {
		fmt.Println("NATIVE_ALPHA_WORKER stream:", scanner.Err())
	}
	if err = cmd.Wait(); err != nil {
		fmt.Println("NATIVE_ALPHA_WORKER stopped:", err)
	}
	close(queue)
	<-done
	return true
}
