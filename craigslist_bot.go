package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	_ "github.com/mattn/go-sqlite3"
)

type Listing struct {
	Title      string
	Price      string
	City       string
	Posted     time.Time
	ListingURL string
}

// Initialize the SQLite3 database
func initDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./craigslist.db")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	// Create table if it doesn't exist
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS listings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT,
		price TEXT,
		city TEXT,
		posted DATETIME,
		listing_url TEXT UNIQUE
	);
	`
	_, err = db.Exec(createTableQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to create table: %v", err)
	}

	return db, nil
}

// Insert a new listing into the database
func insertListing(db *sql.DB, listing Listing) error {
	insertQuery := `
	INSERT INTO listings (title, price, city, posted, listing_url)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(listing_url) DO NOTHING;
	`
	_, err := db.Exec(insertQuery, listing.Title, listing.Price, listing.City, listing.Posted, listing.ListingURL)
	return err
}

// Delete listings older than an hour from the database
func deleteOldListings(db *sql.DB) error {
	deleteQuery := `
	DELETE FROM listings
	WHERE posted < datetime('now', '-1 hour');
	`
	_, err := db.Exec(deleteQuery)
	return err
}

func scrapeListings(ctx context.Context) ([]Listing, error) {
	var listings []Listing

	url := "https://charlotte.craigslist.org/search/sss#search=1~gallery~0~0"

	var htmlContent string

	// Run the chromedp tasks to load the page and wait for the content
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("li.cl-search-result"), // Wait until listings are loaded
		chromedp.InnerHTML("body", &htmlContent),  // Get the full HTML content of the body
	)
	if err != nil {
		return listings, fmt.Errorf("failed to load the page: %v", err)
	}

	// Use goquery to parse the loaded HTML content
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return listings, fmt.Errorf("failed to parse the page: %v", err)
	}

	// Extract listings
	doc.Find("li.cl-search-result").Each(func(i int, s *goquery.Selection) {
		title, exists := s.Attr("title")
		if !exists {
			title = "No title"
		}
		link, exists := s.Find("a").Attr("href")
		if !exists {
			return
		}
		price := strings.TrimSpace(s.Find(".priceinfo").Text())
		metaText := strings.TrimSpace(s.Find(".meta").Text())
		city := strings.Split(metaText, "·")[1] // Assuming city is after the separator

		listing := Listing{
			Title:      title,
			Price:      price,
			City:       strings.TrimSpace(city),
			Posted:     time.Now(),
			ListingURL: link,
		}

		listings = append(listings, listing)
	})

	return listings, nil
}

func sendNotification(message string) {
	ntfyUrl := "https://ntfy.sh/charlottecraig"

	req, err := http.NewRequest("POST", ntfyUrl, strings.NewReader(message))
	if err != nil {
		fmt.Println("Failed to create request:", err)
		return
	}
	req.Header.Set("Title", "Craigslist Alert")
	req.Header.Set("Priority", "high")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Failed to send notification:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Failed to send notification: Status code %d\n", resp.StatusCode)
	} else {
		fmt.Printf("Notification sent: %s\n", message)
	}
}

func main() {
	// Initialize database
	db, err := initDB()
	if err != nil {
		fmt.Printf("Failed to initialize database: %v\n", err)
		return
	}
	defer db.Close()

	// Create a new context with a timeout to ensure we don't wait forever
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	// Loop to check new listings every minute
	checkTicker := time.NewTicker(1 * time.Minute)
	defer checkTicker.Stop()

	for {
		select {
		case <-checkTicker.C:
			listings, err := scrapeListings(ctx)
			if err != nil {
				fmt.Printf("Failed to scrape listings: %v\n", err)
				continue
			}

			for _, listing := range listings {
				// Insert the listing into the database
				err := insertListing(db, listing)
				if err != nil {
					fmt.Printf("Failed to insert listing: %v\n", err)
					continue
				}

				// If the price is free, empty, or unknown, send a notification
				if strings.ToLower(listing.Price) == "free" || listing.Price == "" || listing.Price == "()" {
					sendNotification(fmt.Sprintf("New free or unknown price listing! %s (%s) %s", listing.Title, listing.Price, listing.City))
				}
			}

			// Delete listings older than an hour
			err = deleteOldListings(db)
			if err != nil {
				fmt.Printf("Failed to delete old listings: %v\n", err)
			}

			// Delay to avoid IP bans
			time.Sleep(time.Duration(2+len(listings)%3) * time.Second)
		}
	}
}
