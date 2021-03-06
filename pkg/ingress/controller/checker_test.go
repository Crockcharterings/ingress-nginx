/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/kubernetes/pkg/util/filesystem"

	ngx_config "k8s.io/ingress-nginx/pkg/ingress/controller/config"
)

func TestNginxCheck(t *testing.T) {
	mux := http.NewServeMux()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	}))
	defer server.Close()
	// port to be used in the check
	p := server.Listener.Addr().(*net.TCPAddr).Port

	// mock filesystem
	fs := filesystem.NewFakeFs()

	n := &NGINXController{
		cfg: &Configuration{
			ListenPorts: &ngx_config.ListenPorts{
				Status: p,
			},
		},
		fileSystem: fs,
	}

	t.Run("no pid or process", func(t *testing.T) {
		if err := callHealthz(true, mux); err == nil {
			t.Errorf("expected an error but none returned")
		}
	})

	// create required files
	fs.MkdirAll("/run", 0655)
	pidFile, err := fs.Create("/run/nginx.pid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("no process", func(t *testing.T) {
		if err := callHealthz(true, mux); err == nil {
			t.Errorf("expected an error but none returned")
		}
	})

	// start dummy process to use the PID
	cmd := exec.Command("sleep", "3600")
	cmd.Start()
	pid := cmd.Process.Pid
	defer cmd.Process.Kill()
	go func() {
		cmd.Wait()
	}()

	pidFile.Write([]byte(fmt.Sprintf("%v", pid)))
	pidFile.Close()

	healthz.InstallHandler(mux, n)

	t.Run("valid request", func(t *testing.T) {
		if err := callHealthz(false, mux); err != nil {
			t.Error(err)
		}
	})

	pidFile, err = fs.Create("/run/nginx.pid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pidFile.Write([]byte(fmt.Sprintf("%v", pid)))
	pidFile.Close()

	t.Run("valid request", func(t *testing.T) {
		if err := callHealthz(true, mux); err == nil {
			t.Errorf("expected an error but none returned")
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		n.cfg.ListenPorts.Status = 9000
		if err := callHealthz(true, mux); err == nil {
			t.Errorf("expected an error but none returned")
		}
	})
}

func callHealthz(expErr bool, mux *http.ServeMux) error {
	req, err := http.NewRequest("GET", "http://localhost:8080/healthz", nil)
	if err != nil {
		return err
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if expErr && w.Code != http.StatusInternalServerError {
		return fmt.Errorf("expected an error")
	}

	if w.Body.String() != "ok" {
		return fmt.Errorf("healthz error: %v", w.Body.String())
	}

	if w.Code != http.StatusOK {
		return fmt.Errorf("expected status code 200 but %v returned", w.Code)
	}

	return nil
}
