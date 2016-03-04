package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nlopes/slack"
)

const (
	MessageGroupDisplayTimestampFormat = "3:04pm"
	MessageTextBlockquotePrefix        = "&gt;"
	MessageTextControlRegexp           = "<(.*?)>"
)

type Message struct {
	*slack.Message
	timezoneLocation *time.Location
}

func (m *Message) TimestampTime() time.Time {
	floatTimestamp, err := strconv.ParseFloat(m.Timestamp, 64)
	if err != nil {
		log.Println("Could not parse timestamp \"%s\".", m.Timestamp, err)
		return time.Time{}
	}
	return time.Unix(int64(floatTimestamp), 0).In(m.timezoneLocation)
}

func (m *Message) TextHtml() template.HTML {
	lines := strings.Split(m.Text, "\n")
	htmlPieces := []string{}
	controlRegexp := regexp.MustCompile(MessageTextControlRegexp)
	for _, line := range lines {
		linePrefix := ""
		lineSuffix := ""
		if strings.HasPrefix(line, MessageTextBlockquotePrefix) {
			line = strings.TrimPrefix(line, MessageTextBlockquotePrefix)
			if line == "" {
				// Ensure that even empty blockquote lines get rendered.
				line = "\u200b"
			}
			linePrefix = fmt.Sprintf("<blockquote style='%s'>",
				Style("message.blockquote"))
			lineSuffix = "</blockquote>"
		} else {
			lineSuffix = "<br>"
		}
		line = controlRegexp.ReplaceAllStringFunc(line, func(control string) string {
			control = control[1 : len(control)-1]
			anchorText := ""
			pipeIndex := strings.LastIndex(control, "|")
			if pipeIndex != -1 {
				anchorText = control[pipeIndex+1:]
				control = control[:pipeIndex]
			}
			if anchorText == "" {
				anchorText = control
			}
			return fmt.Sprintf("<a href='%s'>%s</a>", control, anchorText)
		})

		htmlPieces = append(htmlPieces, linePrefix)
		// Slack's API claims that all HTML is already escaped
		htmlPieces = append(htmlPieces, line)
		htmlPieces = append(htmlPieces, lineSuffix)
	}
	return template.HTML(strings.Join(htmlPieces, ""))
}

type MessageGroup struct {
	Messages []*Message
	Author   *slack.User
}

func safeFormattedDate(date string) string {
	// Insert zero-width spaces every few characters so that Apple Data
	// Detectors and Gmail's calendar event dection don't pick up on these
	// dates.
	var buffer bytes.Buffer
	dateLength := len(date)
	for i := 0; i < dateLength; i += 2 {
		if i == dateLength-1 {
			buffer.WriteString(date[i : i+1])
		} else {
			buffer.WriteString(date[i : i+2])
			if date[i] != ' ' && date[i+1] != ' ' && i < dateLength-2 {
				buffer.WriteString("\u200b")
			}
		}
	}
	return buffer.String()
}

func (mg *MessageGroup) shouldContainMessage(message *Message, messageAuthor *slack.User) bool {
	if messageAuthor.ID != mg.Author.ID {
		return false
	}
	lastMessage := mg.Messages[len(mg.Messages)-1]
	timestampDelta := message.TimestampTime().Sub(lastMessage.TimestampTime())
	if timestampDelta > time.Minute*10 {
		return false
	}
	return true
}

func (mg *MessageGroup) DisplayTimestamp() string {
	return safeFormattedDate(mg.Messages[0].TimestampTime().Format(
		MessageGroupDisplayTimestampFormat))
}

func groupMessages(messages []*slack.Message, slackClient *slack.Client, timezoneLocation *time.Location) ([]*MessageGroup, error) {
	var currentGroup *MessageGroup = nil
	groups := make([]*MessageGroup, 0)
	userLookup, err := newUserLookup(slackClient)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		message := &Message{messages[i], timezoneLocation}
		if message.Hidden {
			continue
		}
		messageAuthor, _ := userLookup.GetUserForMessage(messages[i])
		if messageAuthor == nil {
			log.Printf("Could not determine author for message type %s "+
				"(subtype %s), skipping", message.Type, message.SubType)
			continue
		}
		if currentGroup == nil || !currentGroup.shouldContainMessage(message, messageAuthor) {
			currentGroup = &MessageGroup{
				Messages: make([]*Message, 0),
				Author:   messageAuthor,
			}
			groups = append(groups, currentGroup)
		}
		currentGroup.Messages = append(currentGroup.Messages, message)
	}
	return groups, nil
}
