package opskat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestHTTPTransport(t *testing.T) {
	Convey("HTTPTransport", t, func() {
		stub := newHTTPStub()
		SetHostStub(stub)
		defer SetHostStub(nil)

		transport := NewHTTPTransport()
		client := &http.Client{Transport: transport}

		Convey("GET request", func() {
			stub.response = &httpStubResponse{
				status:      200,
				contentType: "text/plain",
				body:        "hello from host",
			}

			resp, err := client.Get("http://example.com/test")
			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, 200)

			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			So(string(body), ShouldEqual, "hello from host")
		})

		Convey("POST request with body", func() {
			stub.response = &httpStubResponse{
				status:      201,
				contentType: "application/json",
				body:        `{"created":true}`,
			}

			resp, err := client.Post("http://example.com/items", "application/json", strings.NewReader(`{"name":"test"}`))
			So(err, ShouldBeNil)
			defer func() { _ = resp.Body.Close() }()
			So(resp.StatusCode, ShouldEqual, 201)

			// Verify the body was written to the handle
			So(string(stub.writtenBody), ShouldEqual, `{"name":"test"}`)
		})
	})
}

// httpStub implements hostCaller for HTTP testing
type httpStubResponse struct {
	status      int
	contentType string
	body        string
}

type httpStub struct {
	nopHost
	response    *httpStubResponse
	writtenBody []byte
	bodyPos     int
}

func newHTTPStub() *httpStub { return &httpStub{} }

func (s *httpStub) IOOpen(params []byte) (uint32, []byte, error) {
	return 1, []byte(`{}`), nil
}

func (s *httpStub) IOWrite(handleID uint32, data []byte) (int, error) {
	s.writtenBody = append(s.writtenBody, data...)
	return len(data), nil
}

func (s *httpStub) IOFlush(handleID uint32) ([]byte, error) {
	if s.response == nil {
		return nil, fmt.Errorf("no response configured")
	}
	meta := IOMeta{
		Status:      s.response.status,
		ContentType: s.response.contentType,
		Size:        int64(len(s.response.body)),
		Headers:     map[string]string{"Content-Type": s.response.contentType},
	}
	metaJSON, _ := json.Marshal(meta)
	return metaJSON, nil
}

func (s *httpStub) IORead(handleID uint32, size int) ([]byte, error) {
	if s.response == nil {
		return nil, io.EOF
	}
	body := []byte(s.response.body)
	if s.bodyPos >= len(body) {
		return nil, io.EOF
	}
	end := s.bodyPos + size
	if end > len(body) {
		end = len(body)
	}
	data := body[s.bodyPos:end]
	s.bodyPos = end
	return data, nil
}

func (s *httpStub) IOClose(handleID uint32) error { return nil }
