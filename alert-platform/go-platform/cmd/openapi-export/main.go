package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/adminapi"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/gateway"
)

func main() {
	service := flag.String("service", "", "OpenAPI document to export: gateway or admin")
	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}

	var document any
	switch *service {
	case "gateway":
		if err := json.Unmarshal(gateway.OpenAPISpec(), &document); err != nil {
			log.Fatalf("decode gateway OpenAPI document: %s", err)
		}
	case "admin":
		document = adminapi.OpenAPISpec()
	default:
		log.Fatalf("unknown service %s; expected gateway or admin", *service)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		log.Fatalf("encode %s OpenAPI document: %s", *service, err)
	}
}
