const puppeteer = require('puppeteer');
const sqlite3 = require('sqlite3').verbose();
const fetch = require('node-fetch');

// Initialize SQLite3 Database
const db = new sqlite3.Database('./craigslist.db', (err) => {
    if (err) {
        console.error('Failed to connect to the database:', err.message);
    } else {
        console.log('Connected to the SQLite database.');
    }
});

// Create listings table if it doesn't exist
db.run(`
    CREATE TABLE IF NOT EXISTS listings (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT,
        price TEXT,
        city TEXT,
        posted DATETIME,
        listing_url TEXT UNIQUE,
        notified BOOLEAN DEFAULT 0
    );
`, (err) => {
    if (err) {
        console.error('Failed to create table:', err.message);
    }
});

// Function to insert a listing into the database
function insertListing(listing) {
    const { title, price, city, posted, listing_url } = listing;
    db.run(`
        INSERT INTO listings (title, price, city, posted, listing_url)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(listing_url) DO NOTHING;
    `, [title, price, city, posted, listing_url], function (err) {
        if (err) {
            console.error('Failed to insert listing:', err.message);
        } else if (this.changes > 0) {
            // Notify if the item is free or the price is unknown and it's new
            if (!price || price.toLowerCase() === 'free' || price === '()') {
                sendNotification(listing);
                markAsNotified(listing_url);
            }
        }
    });
}

// Function to mark a listing as notified
function markAsNotified(listing_url) {
    db.run(`
        UPDATE listings
        SET notified = 1
        WHERE listing_url = ?;
    `, [listing_url], (err) => {
        if (err) {
            console.error('Failed to mark as notified:', err.message);
        }
    });
}

// Function to delete listings older than an hour
function deleteOldListings() {
    db.run(`
        DELETE FROM listings
        WHERE posted < datetime('now', '-1 hour');
    `, (err) => {
        if (err) {
            console.error('Failed to delete old listings:', err.message);
        }
    });
}

// Function to send a notification via ntfy using fetch with action buttons
function sendNotification(listing) {
    const { title, price, city, listing_url } = listing;
    const message = `${title} (${price || 'Unknown Price'}) ${city}`;
    
    fetch('https://ntfy.sh/charlottecraig', {
        method: 'POST',
        body: message,
        headers: {
            'Title': 'New Craigslist Listing Alert',
            'Priority': 'high',
            'Actions': `view, Open Listing, ${listing_url}, clear=true`
        }
    })
    .then(response => response.text())
    .then(result => console.log('Notification sent:', result))
    .catch(error => console.error('Error sending notification:', error));
}

// Function to scrape Craigslist listings using Puppeteer
async function scrapeListings() {
    const browser = await puppeteer.launch({
        headless: true,
        executablePath: '/usr/bin/google-chrome',
        args: ['--no-sandbox', '--disable-setuid-sandbox'],
    });

    const page = await browser.newPage();

    // Navigate to the Craigslist search page
    await page.goto('https://charlotte.craigslist.org/search/sss#search=1~gallery~0~0', { waitUntil: 'networkidle2' });

    // Wait for up to 10 seconds for the page to fully load
    await page.waitForTimeout(10000);

    // Extract listings from the page
    const listings = await page.evaluate(() => {
        const items = [];
        document.querySelectorAll('li.cl-search-result').forEach((item) => {
            const title = item.getAttribute('title') || 'No title';
            const price = item.querySelector('.priceinfo') ? item.querySelector('.priceinfo').textContent.trim() : '';
            const metaText = item.querySelector('.meta') ? item.querySelector('.meta').textContent.trim() : '';
            const city = metaText.split('·')[1] ? metaText.split('·')[1].trim() : 'Unknown City';
            const posted = metaText.split('·')[0] ? metaText.split('·')[0].trim() : '';
            const link = item.querySelector('a') ? item.querySelector('a').getAttribute('href') : '';

            // Push the extracted information to the items array
            items.push({
                title,
                price,
                city,
                posted,
                listing_url: link ? `${link}` : ''
            });
        });
        return items;
    });

    // Close the browser
    await browser.close();

    return listings;
}

// Main function to handle the periodic scraping and database updates
async function main() {
    // Run the initial scrape and processing
    await processListings();

    // Schedule the scraping and database maintenance tasks to run every minute
    setInterval(async () => {
        await processListings();
        deleteOldListings();
    }, 60 * 1000); // Run every minute
}

// Function to process the listings by inserting them into the database
async function processListings() {
    const listings = await scrapeListings();
    for (const listing of listings) {
        insertListing(listing);
    }
}

// Start the process
main();
