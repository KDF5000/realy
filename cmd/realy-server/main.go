package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/KDF5000/realy/controlplane"
	controlplanepostgres "github.com/KDF5000/realy/controlplane/postgres"
	"github.com/KDF5000/realy/transport/httpapi"
)

func main() {
	listen := flag.String("listen", defaultListenAddress(), "HTTP listen address (env: REALY_LISTEN or PORT)")
	leaseTTL := flag.Duration("lease-ttl", 30*time.Second, "assignment lease duration")
	nodeTimeout := flag.Duration("node-timeout", 15*time.Second, "time without heartbeat before a node is offline")
	reconcileInterval := flag.Duration("reconcile-interval", 2*time.Second, "expired attempt recovery interval")
	databaseURL := flag.String("database-url", os.Getenv("REALY_DATABASE_URL"), "PostgreSQL URL (env: REALY_DATABASE_URL)")
	memory := flag.Bool("memory", false, "use ephemeral in-memory storage for tests and demos")
	artifactRoot := flag.String("artifact-root", envOr("REALY_ARTIFACT_ROOT", "./.realy/artifacts"), "local artifact blob directory")
	artifactBackend := flag.String("artifact-backend", envOr("REALY_ARTIFACT_BACKEND", "file"), "artifact backend: file or s3")
	hostToken := flag.String("host-token", os.Getenv("REALY_HOST_TOKEN"), "host bearer token")
	nodeToken := flag.String("node-token", os.Getenv("REALY_NODE_TOKEN"), "node bearer token")
	tenantID := flag.String("tenant", envOr("REALY_TENANT_ID", "default"), "tenant assigned to the host token")
	projectID := flag.String("project", envOr("REALY_PROJECT_ID", "default"), "project assigned to the host token")
	flag.Parse()
	var blobs controlplane.BlobStore
	var err error
	if *artifactBackend == "s3" {
		blobs, err = controlplane.NewS3BlobStore(controlplane.S3BlobOptions{Endpoint: os.Getenv("REALY_S3_ENDPOINT"), AccessKey: os.Getenv("REALY_S3_ACCESS_KEY"), SecretKey: os.Getenv("REALY_S3_SECRET_KEY"), SessionToken: os.Getenv("REALY_S3_SESSION_TOKEN"), Bucket: os.Getenv("REALY_S3_BUCKET"), Prefix: os.Getenv("REALY_S3_PREFIX"), Region: os.Getenv("REALY_S3_REGION"), Secure: os.Getenv("REALY_S3_SECURE") != "false"})
	} else if *artifactBackend == "file" {
		blobs, err = controlplane.NewFileBlobStore(*artifactRoot)
	} else {
		log.Fatalf("unsupported artifact backend %q", *artifactBackend)
	}
	if err != nil {
		log.Fatal(err)
	}
	var service *controlplane.Service
	options := controlplane.Options{LeaseTTL: *leaseTTL, NodeOfflineAfter: *nodeTimeout, BlobStore: blobs}
	if *memory {
		service = controlplane.NewWithOptions(controlplane.NewMemoryStorage(), options)
	} else {
		store, err := controlplanepostgres.Open(context.Background(), *databaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer store.Close()
		service = controlplane.NewWithOptions(store, options)
	}
	if *reconcileInterval <= 0 {
		log.Fatal("reconcile interval must be positive")
	}
	go func() {
		ticker := time.NewTicker(*reconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			recovered, err := service.Reconcile(context.Background(), 100)
			if err != nil {
				log.Printf("reconcile expired attempts: %v", err)
			} else if recovered > 0 {
				log.Printf("recovered %d expired attempt(s)", recovered)
			}
		}
	}()
	var handler http.Handler = httpapi.NewHandler(service)
	if *hostToken != "" || *nodeToken != "" {
		if *hostToken == "" || *nodeToken == "" {
			log.Fatal("host-token and node-token must both be configured")
		}
		handler = httpapi.NewHandlerWithAuth(service, httpapi.StaticTokens{{Value: *hostToken, Scope: controlplane.AccessScope{Kind: controlplane.AccessHost, TenantID: *tenantID, ProjectID: *projectID}}, {Value: *nodeToken, Scope: controlplane.AccessScope{Kind: controlplane.AccessNode}}})
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Realy control plane listening on %s", *listen)
	log.Fatal(server.ListenAndServe())
}

func defaultListenAddress() string {
	if value := os.Getenv("REALY_LISTEN"); value != "" {
		return value
	}
	if value := os.Getenv("PORT"); value != "" {
		return ":" + value
	}
	return ":8787"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
