// Package main implements a simple web scraper that:
// 1. Downloads HTML pages from the Nepal Law Commission website.
// 2. Extracts category links from the main listing page.
// 3. Paginates through each category (?page=2, ?page=3, ...) until a 404 page is hit.
// 4. Finds PDF document links inside every category page.
// 5. Downloads discovered PDF files locally.
package main

import (
	"fmt"           // Creates formatted errors.
	"io"            // Reads response bodies and copies file data.
	"log"           // Prints progress and error messages.
	"net/http"      // Sends HTTP requests.
	"net/url"       // Decodes URL encoded filenames and builds paginated URLs.
	"os"            // Creates folders and files.
	"path/filepath" // Handles file paths safely.
	"strings"       // Provides string manipulation helpers.
	"time"          // Provides HTTP timeout values.

	"golang.org/x/net/html" // Parses HTML documents into node trees.
)

// createHTTPClient creates a reusable HTTP client.
//
// A shared client allows HTTP connections to be reused,
// which improves performance when making many requests.
func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second, // Prevent requests from hanging forever.
	}
}

// fetchHTMLStatus downloads HTML content from a webpage and returns the
// HTTP status code alongside the body.
//
// Unlike a plain "fetch or fail" helper, this deliberately does NOT treat
// a non-200 status as an error, since callers doing pagination need to
// inspect 404 responses instead of aborting on them.
func fetchHTMLStatus(client *http.Client, pageURL string) (int, string, error) {
	// Create a GET request for the target URL.
	request, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("creating request: %w", err)
	}

	// Add a browser-like user agent to reduce basic scraper blocking.
	request.Header.Set("User-Agent", "Mozilla/5.0 Go scraper")

	// Send the HTTP request.
	response, err := client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	// Read the complete HTML response regardless of status code.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("reading response: %w", err)
	}

	return response.StatusCode, string(body), nil
}

// fetchHTML downloads HTML content from a webpage and errors on non-200
// responses. Kept for pages where we genuinely want to fail fast (e.g. the
// main listing page must load correctly or nothing else can proceed).
func fetchHTML(client *http.Client, pageURL string) (string, error) {
	statusCode, body, err := fetchHTMLStatus(client, pageURL)
	if err != nil {
		return "", err
	}

	if statusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", statusCode)
	}

	return body, nil
}

// isPageNotFound reports whether a fetched page should be treated as the
// end of pagination.
//
// Two signals are checked because sites are inconsistent about this:
// some correctly return HTTP 404, others return HTTP 200 with a page
// whose <title> says "Error 404".
func isPageNotFound(statusCode int, htmlContent string) bool {
	if statusCode == http.StatusNotFound {
		return true
	}

	return strings.Contains(htmlContent, "<title>Error 404</title>")
}

// walkHTML recursively walks the entire HTML tree.
//
// The handler function is executed for every node.
// Traversal uses depth-first order:
// parent node -> first child -> child descendants -> next sibling.
func walkHTML(node *html.Node, handler func(*html.Node)) {
	handler(node)

	// Visit each child node recursively.
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, handler)
	}
}

// extractLinks extracts all href values from an HTML document.
func extractLinks(htmlContent string) ([]string, error) {
	// Convert raw HTML into a parsed node tree.
	document, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parsing html: %w", err)
	}

	var links []string

	// Visit every node and find anchor tags.
	walkHTML(document, func(node *html.Node) {
		// Ignore anything that is not an <a> element.
		if node.Type != html.ElementNode || node.Data != "a" {
			return
		}

		// Check anchor attributes for href.
		for _, attribute := range node.Attr {
			if attribute.Key != "href" {
				continue
			}

			// Remove surrounding whitespace.
			link := strings.TrimSpace(attribute.Val)

			// Store valid links.
			if link != "" {
				links = append(links, link)
			}

			// Stop after processing href.
			break
		}
	})

	return links, nil
}

// removeDuplicates removes repeated strings while preserving order.
func removeDuplicates(items []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(items))

	for _, item := range items {
		// Skip values already encountered.
		if _, exists := seen[item]; exists {
			continue
		}

		seen[item] = struct{}{}
		result = append(result, item)
	}

	return result
}

// extractCategoryURLs extracts category page URLs from the main page HTML.
//
// Only URLs beginning with the category prefix are kept.
func extractCategoryURLs(htmlContent string) ([]string, error) {
	links, err := extractLinks(htmlContent)
	if err != nil {
		return nil, err
	}

	const categoryPrefix = "https://lawcommission.gov.np/category/"

	var categories []string

	for _, link := range links {
		// Keep only category pages.
		if strings.HasPrefix(link, categoryPrefix) {
			categories = append(categories, link)
		}
	}

	// Remove repeated category links.
	return removeDuplicates(categories), nil
}

// extractPDFURLs extracts PDF download URLs from HTML.
//
// The website stores PDF files under a fixed media directory,
// so only matching URLs are returned.
func extractPDFURLs(htmlContent string) ([]string, error) {
	links, err := extractLinks(htmlContent)
	if err != nil {
		return nil, err
	}

	const pdfPrefix = "https://giwmscdnone.gov.np/media/pdf_upload/"

	var pdfs []string

	for _, link := range links {
		// Keep only PDF download links.
		if strings.HasPrefix(link, pdfPrefix) {
			pdfs = append(pdfs, link)
		}
	}

	// Avoid downloading the same PDF multiple times.
	return removeDuplicates(pdfs), nil
}

// folderExists checks whether a directory exists.
func folderExists(folderPath string) bool {
	info, err := os.Stat(folderPath)

	// Missing folders or filesystem errors are treated as not existing.
	if err != nil {
		return false
	}

	return info.IsDir()
}

// createFolder creates a directory if it does not already exist.
func createFolder(folderPath string) error {
	// Do nothing if the folder already exists.
	if folderExists(folderPath) {
		return nil
	}

	// Create folder and any missing parent directories.
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		return fmt.Errorf("creating folder: %w", err)
	}

	return nil
}

// pdfExists checks whether a PDF file already exists.
//
// Existing files are skipped to avoid unnecessary downloads.
func pdfExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// createFilename converts a PDF URL into a readable local filename.
func createFilename(pdfURL string) string {
	// Extract the filename portion from the URL.
	filename := filepath.Base(pdfURL)

	// Decode escaped characters such as %20.
	decoded, err := url.QueryUnescape(filename)
	if err == nil {
		return decoded
	}

	// Return original filename if decoding fails.
	return filename
}

// downloadPDF downloads a single PDF file.
//
// Existing files are skipped so interrupted runs can continue safely.
func downloadPDF(client *http.Client, pdfURL string, folder string) error {
	filename := createFilename(pdfURL)
	filePath := filepath.Join(folder, filename)

	// Avoid downloading files that already exist.
	if pdfExists(filePath) {
		log.Printf("Skipping existing PDF: %s", filename)
		return nil
	}

	// Create HTTP request.
	request, err := http.NewRequest(http.MethodGet, pdfURL, nil)
	if err != nil {
		return err
	}

	// Add browser-like user agent.
	request.Header.Set("User-Agent", "Mozilla/5.0 Go scraper")

	// Download the PDF.
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Create the local output file.
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Stream downloaded data directly into the file.
	if _, err := io.Copy(file, response.Body); err != nil {
		return err
	}

	log.Printf("Downloaded: %s", filename)

	return nil
}

// downloadPDFs downloads every PDF URL into the PDFs folder.
//
// If one PDF fails, the error is logged and downloading continues
// for the remaining files.
func downloadPDFs(client *http.Client, pdfURLs []string) error {
	const folder = "PDFs"

	// Ensure the download directory exists.
	if err := createFolder(folder); err != nil {
		return err
	}

	// Process every PDF URL individually.
	for _, pdfURL := range pdfURLs {
		if err := downloadPDF(client, pdfURL, folder); err != nil {
			log.Printf("Failed %s: %v", pdfURL, err)
		}
	}

	return nil
}

// buildPageURL builds the URL for a given page number of a category.
//
// Page 1 is the bare category URL (no query string), matching how the
// site links to it from the main listing page. Page 2 onward appends
// ?page=N.
func buildPageURL(categoryURL string, page int) (string, error) {
	if page <= 1 {
		return categoryURL, nil
	}

	parsed, err := url.Parse(categoryURL)
	if err != nil {
		return "", fmt.Errorf("parsing category url: %w", err)
	}

	query := parsed.Query()
	query.Set("page", fmt.Sprintf("%d", page))
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

// scrapeCategory walks every page of a single category, starting at page 1
// and incrementing until a 404 page is encountered. All PDFs found along
// the way are downloaded.
func scrapeCategory(client *http.Client, categoryURL string) {
	for page := 1; ; page++ {
		pageURL, err := buildPageURL(categoryURL, page)
		if err != nil {
			log.Println(err)
			return
		}

		statusCode, pageHTML, err := fetchHTMLStatus(client, pageURL)
		if err != nil {
			log.Printf("Failed to fetch %s: %v", pageURL, err)
			return
		}

		// Stop paginating once we hit the 404 page.
		if isPageNotFound(statusCode, pageHTML) {
			log.Printf("Reached end of pagination for %s (stopped at page %d)", categoryURL, page)
			return
		}

		log.Printf("Scraping %s", pageURL)

		// Extract PDF links from this page.
		pdfURLs, err := extractPDFURLs(pageHTML)
		if err != nil {
			log.Println(err)
			continue
		}

		// Download all discovered PDFs from this page.
		if err := downloadPDFs(client, pdfURLs); err != nil {
			log.Println(err)
		}
	}
}

func main() {
	// Create a reusable HTTP client for all requests.
	client := createHTTPClient()

	// Main page containing links to document categories.
	const mainPage = "https://www.lawcommission.gov.np/pages/list-volume-act/"

	// Download the main listing page.
	htmlContent, err := fetchHTML(client, mainPage)
	if err != nil {
		log.Fatal(err)
	}

	// Extract category pages from the main page.
	categoryURLs, err := extractCategoryURLs(htmlContent)
	if err != nil {
		log.Fatal(err)
	}

	// Process each category page, paginating through every page until 404.
	for _, categoryURL := range categoryURLs {
		scrapeCategory(client, categoryURL)
	}
}
