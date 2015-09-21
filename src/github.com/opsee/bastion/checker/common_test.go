package checker

import (
	"fmt"
	"net/http"

	"github.com/op/go-logging"
)

const (
	testHTTPResponseString = "OK"
	testHTTPServerPort     = 40000
)

func httpCheckStub() *HttpCheck {
	return &HttpCheck{
		Name:     "test check",
		Path:     "/",
		Protocol: "http",
		Port:     testHTTPServerPort,
		Verb:     "GET",
	}
}

func testCheckStub() *Check {
	return &Check{
		Id:        "string",
		Interval:  60,
		Target:    &Target{},
		CheckSpec: &Any{},
	}
}

type testResolver struct {
	Targets   map[string][]*Target
	Instances map[string][]*Target
}

func (t *testResolver) Resolve(tgt *Target) ([]*Target, error) {
	logger.Debug("Resolving target: %s", tgt)
	if tgt.Id == "empty" {
		return []*Target{}, nil
	}

	if tgt.Type == "instance" {
		return t.Instances[tgt.Id], nil
	}
	return t.Targets[tgt.Id], nil
}

func newTestResolver() *testResolver {
	addr := "127.0.0.1"
	return &testResolver{
		Targets: map[string][]*Target{
			"sg": []*Target{
				&Target{
					Id:   "id",
					Type: "instance",
				},
			},
			"sg3": []*Target{
				&Target{
					Id:   "id",
					Name: "id",
					Type: "instance",
				},
				&Target{
					Id:   "id",
					Name: "id",
					Type: "instance",
				},
				&Target{
					Id:   "id",
					Name: "id",
					Type: "instance",
				},
			},
		},
		Instances: map[string][]*Target{
			"id": []*Target{
				&Target{
					Type: "ip",
					Id:   addr,
				}},
		},
	}
}

func testMakePassingTestCheck() *Check {
	check := testCheckStub()
	check.Target = &Target{
		Type: "sg",
		Id:   "sg",
		Name: "sg",
	}

	spec, _ := MarshalAny(httpCheckStub())
	check.CheckSpec = spec
	return check
}

func init() {
	logging.SetLevel(logging.GetLevel("DEBUG"), "checker")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Handling request: %s", *r)
		headerMap := w.Header()
		headerMap[testHTTPHeaderKey] = []string{testHTTPHeaderValue}
		w.WriteHeader(200)
		w.Write([]byte(testHTTPResponseString))
	})
	errChan := make(chan error, 1)
	go func() {
		errChan <- http.ListenAndServe(fmt.Sprintf(":%d", testHTTPServerPort), nil)
	}()
}
