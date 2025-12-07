package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

var ticker *time.Ticker
var stopChan chan bool
var currentGuildID, currentMessage string

func main() {
	// Get token from environment variable for security
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN environment variable not set")
	}

	// Create a new Discord session
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	// Add handlers
	dg.AddHandler(messageCreate)

	// Open the websocket connection to Discord
	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}
	defer dg.Close()

	// Start the HTTP server in a goroutine to keep the service alive on Render
	go startHTTPServer()

	// Wait for termination signal
	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

// Handler for messageCreate events
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Respond to "!ping" command
	if strings.HasPrefix(m.Content, "!ping") {
		s.ChannelMessageSend(m.ChannelID, "Pong!")
	}

	// Handle one-time mass DM command: !massdm <guild_id> <message>
	if strings.HasPrefix(m.Content, "!massdm ") {
		parts := strings.SplitN(m.Content, " ", 3)
		if len(parts) < 3 {
			s.ChannelMessageSend(m.ChannelID, "Usage: !massdm <guild_id> <message>")
			return
		}
		guildID := parts[1]
		message := parts[2]
		go massDM(s, guildID, message, m.ChannelID) // Run in goroutine to avoid blocking
	}

	// Handle start periodic mass DM: !startmassdm <guild_id> <message>
	if strings.HasPrefix(m.Content, "!startmassdm ") {
		if ticker != nil {
			s.ChannelMessageSend(m.ChannelID, "Periodic mass DM already running. Use !stopmassdm to stop it first.")
			return
		}
		parts := strings.SplitN(m.Content, " ", 3)
		if len(parts) < 3 {
			s.ChannelMessageSend(m.ChannelID, "Usage: !startmassdm <guild_id> <message>")
			return
		}
		currentGuildID = parts[1]
		currentMessage = parts[2]
		stopChan = make(chan bool)
		ticker = time.NewTicker(2 * time.Minute) // Every 2 minutes
		go func() {
			for {
				select {
				case <-ticker.C:
					massDM(s, currentGuildID, currentMessage, m.ChannelID)
				case <-stopChan:
					ticker.Stop()
					ticker = nil
					s.ChannelMessageSend(m.ChannelID, "Periodic mass DM stopped.")
					return
				}
			}
		}()
		s.ChannelMessageSend(m.ChannelID, "Periodic mass DM started. Sending every 2 minutes.")
	}

	// Handle stop periodic mass DM: !stopmassdm
	if m.Content == "!stopmassdm" {
		if ticker == nil {
			s.ChannelMessageSend(m.ChannelID, "No periodic mass DM is running.")
			return
		}
		stopChan <- true
	}
}

// Mass DM function with rate limiting optimizations
func massDM(s *discordgo.Session, guildID, message, replyChannelID string) {
	// Fetch guild members (requires permission; may fail if not admin)
	members, err := s.GuildMembers(guildID, "", 1000) // Fetch up to 1000 members
	if err != nil {
		s.ChannelMessageSend(replyChannelID, fmt.Sprintf("Error fetching members: %v", err))
		return
	}

	s.ChannelMessageSend(replyChannelID, fmt.Sprintf("Starting mass DM to %d members. This may take time.", len(members)))

	successCount := 0
	for _, member := range members {
		// Skip bots
		if member.User.Bot {
			continue
		}

		// Create DM channel
		channel, err := s.UserChannelCreate(member.User.ID)
		if err != nil {
			log.Printf("Error creating DM with %s: %v", member.User.Username, err)
			continue
		}

		// Send message with delay to avoid rate limits
		_, err = s.ChannelMessageSend(channel.ID, message)
		if err != nil {
			log.Printf("Error sending DM to %s: %v", member.User.Username, err)
			// If rate limited, wait longer
			if strings.Contains(err.Error(), "429") {
				time.Sleep(60 * time.Second) // Wait 1 minute on rate limit
			}
		} else {
			successCount++
		}

		// Delay between sends (1-2 seconds to stay under limits)
		time.Sleep(1500 * time.Millisecond)
	}

	s.ChannelMessageSend(replyChannelID, fmt.Sprintf("Mass DM complete. Sent to %d/%d members.", successCount, len(members)))
}

// Starts a simple HTTP server to keep the Render service alive
func startHTTPServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Selfbot is alive!")
	})

	fmt.Printf("HTTP server starting on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
