// Temporary utility: list the run artifacts stored in the project bucket, so the
// exported Markdown/PDF files from previous runs can be compared. Not part of the
// service and deleted after use.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type object struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	Updated string `json:"updated"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/devstorage.read_write")
	if err != nil {
		fmt.Println("credentials:", err)
		os.Exit(1)
	}
	client := oauth2.NewClient(ctx, credentials.TokenSource)

	bucket := "crystal-crm-store"
	prefix := "WebSearch_GO/"
	if len(os.Args) > 1 {
		prefix = os.Args[1]
	}
	endpoint := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&maxResults=1000",
		url.PathEscape(bucket), url.QueryEscape(prefix))

	response, err := client.Get(endpoint)
	if err != nil {
		fmt.Println("list:", err)
		os.Exit(1)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		fmt.Printf("list: status %d: %s\n", response.StatusCode, body)
		os.Exit(1)
	}
	var listing struct {
		Items []object `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listing); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	sort.Slice(listing.Items, func(i, j int) bool { return listing.Items[i].Updated < listing.Items[j].Updated })
	fmt.Printf("%d object(s) under %s\n\n", len(listing.Items), bucket+"/"+prefix)
	for _, item := range listing.Items {
		fmt.Printf("%-74s %9s  %s\n", item.Name, item.Size, item.Updated)
	}
}
