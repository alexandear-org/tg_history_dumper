package main

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	maxLongestMessages = 10
	maxPreviewRunes    = 300
	minWordLength      = 4
	maxLeaderboardSize = 20
	maxReactedMessages = 10
	maxTopEmojis       = 15
)

var (
	instagramDomains = []string{"instagram.com", "ddinstagram.com"}
	youtubeDomains   = []string{"youtube.com", "youtu.be"}

	monthsUA = map[time.Month]string{
		time.January:   "січ",
		time.February:  "лют",
		time.March:     "бер",
		time.April:     "кві",
		time.May:       "тра",
		time.June:      "чер",
		time.July:      "лип",
		time.August:    "сер",
		time.September: "вер",
		time.October:   "жов",
		time.November:  "лис",
		time.December:  "гру",
	}

	monthsFullUA = map[time.Month]string{
		time.January:   "Січень",
		time.February:  "Лютий",
		time.March:     "Березень",
		time.April:     "Квітень",
		time.May:       "Травень",
		time.June:      "Червень",
		time.July:      "Липень",
		time.August:    "Серпень",
		time.September: "Вересень",
		time.October:   "Жовтень",
		time.November:  "Листопад",
		time.December:  "Грудень",
	}

	weekdaysUA = map[time.Weekday]string{
		time.Monday:    "Понеділок",
		time.Tuesday:   "Вівторок",
		time.Wednesday: "Середа",
		time.Thursday:  "Четвер",
		time.Friday:    "П'ятниця",
		time.Saturday:  "Субота",
		time.Sunday:    "Неділя",
	}
)

func runYearInReview(saver *JSONFilesHistorySaver, year int, chatID int64) (string, error) {
	chatEntries, err := saver.ReadSavedChatsList()
	if err != nil {
		return "", err
	}

	userReader := NewChatSyncReader[UserData](saver.usersFPath())
	chatReader := NewChatSyncReader[ChatData](saver.chatsFPath())

	if err := userReader.UpdateOffsets(); err != nil {
		return "", err
	}
	if err := chatReader.UpdateOffsets(); err != nil {
		return "", err
	}

	chatsMsgReader := &ChatsMessageReader{}
	server := &Server{}

	stats := newYearStats()
	userCache := &ChatCachedReader[UserData]{reader: userReader}
	total := 0
	headerTitle := ""
	for _, chatEntry := range chatEntries {
		if chatID != 0 && chatEntry.ID != chatID {
			continue
		}

		title, err := server.readChatTitle(userReader, chatReader, chatEntry.ID, chatEntry.FSTitle)
		if err != nil {
			return "", err
		}
		if chatID != 0 {
			headerTitle = title
		}

		msgs, err := messagesInYear(chatsMsgReader, chatEntry.FPath, year)
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			continue
		}

		for _, msg := range msgs {
			stats.addMessage(chatEntry.ID, msg)
		}

		total += len(msgs)
	}

	var buf strings.Builder

	participants := stats.participantCount()
	total = stats.totalMessages
	month, monthCount := stats.mostActiveMonth()
	leastMonth, leastMonthCount := stats.leastActiveMonth()
	day, dayCount := stats.mostActiveDay()
	leastDay, leastDayCount := stats.leastActiveDay()
	weekday, weekdayCount := stats.mostActiveWeekday()
	hour, hourCount := stats.peakHour()
	median := stats.medianMsgsPerPerson()
	joinedNames := stats.namesWithDates(userCache, stats.joiners)
	leavedNames := stats.namesWithDates(userCache, stats.leavers)
	leaderboard := stats.leaderboard(userCache, maxLeaderboardSize)
	awards := stats.funAwards(userCache)

	monthLabel := "n/a"
	if monthCount > 0 {
		monthLabel = monthsFullUA[month]
		if chatID, msgID, found := stats.firstMessageOfMonth(month); found {
			monthLink := formatMessageLink(chatID, msgID)
			monthLabel = fmt.Sprintf("[%s](%s)", monthsFullUA[month], monthLink)
		}
	}
	leastMonthLabel := "n/a"
	if leastMonthCount > 0 {
		leastMonthLabel = fmt.Sprintf("%s (%d пов.)", monthsFullUA[leastMonth], leastMonthCount)
		if chatID, msgID, found := stats.firstMessageOfMonth(leastMonth); found {
			monthLink := formatMessageLink(chatID, msgID)
			leastMonthLabel = fmt.Sprintf("[%s](%s) (%d пов.)", monthsFullUA[leastMonth], monthLink, leastMonthCount)
		}
	}
	dayLabel := "n/a"
	if dayCount > 0 {
		dayLabel = fmt.Sprintf("%d %s", day.Day(), monthsUA[day.Month()])
		if chatID, msgID, found := stats.firstMessageOfDay(day); found {
			dayLink := formatMessageLink(chatID, msgID)
			dayLabel = fmt.Sprintf("[%d %s](%s)", day.Day(), monthsUA[day.Month()], dayLink)
		}
	}
	leastDayLabel := "n/a"
	if leastDayCount > 0 {
		leastDayLabel = fmt.Sprintf("%d %s (%d пов.)", leastDay.Day(), monthsUA[leastDay.Month()], leastDayCount)
		if chatID, msgID, found := stats.firstMessageOfDay(leastDay); found {
			dayLink := formatMessageLink(chatID, msgID)
			leastDayLabel = fmt.Sprintf("[%d %s](%s) (%d пов.)", leastDay.Day(), monthsUA[leastDay.Month()], dayLink, leastDayCount)
		}
	}
	weekdayLabel := "n/a"
	if weekdayCount > 0 {
		weekdayLabel = weekdaysUA[weekday]
	}
	hourLabel := "n/a"
	if hour >= 0 {
		hourLabel = fmt.Sprintf("%02d:00", hour)
	}

	if chatID != 0 {
		if headerTitle != "" {
			fmt.Fprintf(&buf, "# Підсумки року — %d для %s (#%d)\n\n", year, headerTitle, chatID)
		} else {
			fmt.Fprintf(&buf, "# Підсумки року — %d для чату #%d\n\n", year, chatID)
		}
	} else {
		fmt.Fprintf(&buf, "# Підсумки року — %d\n\n", year)
	}

	fmt.Fprint(&buf, "## 📊 Головне за рік\n\n")
	fmt.Fprintf(&buf, "- **Усього повідомлень:** %d\n", total)
	fmt.Fprintf(&buf, "- **Учасників:** %d\n", participants)
	if len(leavedNames) > 0 {
		fmt.Fprintf(&buf, "- **Покинули чат: %d:** %s\n", len(leavedNames), strings.Join(leavedNames, ", "))
	}
	if len(joinedNames) > 0 {
		fmt.Fprintf(&buf, "- **Приєдналися: %d:** %s\n", len(joinedNames), strings.Join(joinedNames, ", "))
	}
	fmt.Fprintf(&buf, "- **Найгарячіший місяць:** %s (%d пов.)\n", monthLabel, monthCount)
	fmt.Fprintf(&buf, "- **Найспокійніший місяць:** %s\n", leastMonthLabel)
	fmt.Fprintf(&buf, "- **Найгарячіший день:** %s (%d пов.)\n", dayLabel, dayCount)
	fmt.Fprintf(&buf, "- **Найспокійніший день:** %s\n", leastDayLabel)
	fmt.Fprintf(&buf, "- **Найактивніший день тижня:** %s (%d пов.)\n", weekdayLabel, weekdayCount)
	fmt.Fprintf(&buf, "- **Піковий час:** %s (%d пов.)\n", hourLabel, hourCount)
	fmt.Fprintf(&buf, "- **Медіана повідомлень на людину:** %d\n", int(median))
	fmt.Fprint(&buf, "\n")

	pdfPageBreak := func(w io.Writer) {
		fmt.Fprintf(w, `<div style="page-break-after: always;"></div>`+"\n\n")
	}

	if len(leaderboard) > 0 {
		fmt.Fprintf(&buf, "## 🏅 Топ балакучих\n\n")
		for i, line := range leaderboard {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if rxnReceivedBoard := stats.reactionReceivedLeaderboard(userCache, maxLeaderboardSize); len(rxnReceivedBoard) > 0 {
		fmt.Fprintf(&buf, "## 👍 Топ за отриманими реакціями\n\n")
		for i, line := range rxnReceivedBoard {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if rxnSentBoard := stats.reactionSentLeaderboard(userCache, maxLeaderboardSize); len(rxnSentBoard) > 0 {
		fmt.Fprintf(&buf, "## 💬 Топ за надісланими реакціями\n\n")
		for i, line := range rxnSentBoard {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, line)
		}
		fmt.Fprint(&buf, "\n")
		pdfPageBreak(&buf)
	}

	if len(awards) > 0 {
		fmt.Fprintf(&buf, "## 🎉 Веселі нагороди\n\n")
		for _, line := range awards {
			fmt.Fprintf(&buf, "- %s\n", line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if headerEmoji, tokens := stats.topEmojis(maxTopEmojis); len(tokens) > 0 {
		fmt.Fprintf(&buf, "## 😊 Топ емодзі %s\n\n", headerEmoji)
		fmt.Fprintf(&buf, "`%s`\n\n", strings.Join(tokens, " "))
	}

	if topWords := stats.topWords(20); len(topWords) > 0 {
		fmt.Fprint(&buf, "## 📝 Топ слів (без поширених стоп-слів)\n\n")
		for _, line := range topWords {
			fmt.Fprintf(&buf, "- %s\n", line)
		}
		fmt.Fprint(&buf, "\n")
		pdfPageBreak(&buf)
	}

	if longest := stats.getTopLongest(maxLongestMessages); len(longest) > 0 {
		fmt.Fprint(&buf, "## 📜 Найдовші повідомлення (за кількістю символів)\n\n")
		for _, lm := range longest {
			name := formatUserName(userCache, lm.userID)
			nameLink := formatUserLink(userCache, lm.userID, name)
			msgLink := formatMessageLink(lm.chatID, lm.msgID)
			dateStr := formatDateTimeUA(lm.date)
			fmt.Fprintf(&buf, "**%s** [_%s_](%s) — %s симв.\n\n", nameLink, dateStr, msgLink, formatThousands(lm.chars))
			fmt.Fprintf(&buf, "> %s\n\n", formatMessagePreview(lm.text, maxPreviewRunes))
		}
	}

	if topReacted := stats.getTopReactedMessages(maxReactedMessages); len(topReacted) > 0 {
		fmt.Fprint(&buf, "## 🔥 Найбільш реактивні повідомлення\n\n")
		for i, rm := range topReacted {
			name := formatUserName(userCache, rm.userID)
			nameLink := formatUserLink(userCache, rm.userID, name)
			msgLink := formatMessageLink(rm.chatID, rm.msgID)
			dateStr := formatDateTimeUA(rm.date)
			fmt.Fprintf(&buf, "%d. **%s** [_%s_](%s) — %d реакцій\n\n", i+1, nameLink, dateStr, msgLink, rm.reactionCount)
			if rm.strippedImageData != "" {
				fmt.Fprintf(&buf, "![preview](%s)\n\n", formatStrippedImageDataURI(rm.strippedImageData))
			}
			fmt.Fprintf(&buf, "> %s\n\n", formatMessagePreview(rm.text, maxPreviewRunes))
		}
	}

	return buf.String(), nil
}

func messagesInYear(r *ChatsMessageReader, chatPath string, year int) ([]map[string]any, error) {
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)

	var keep []map[string]any
	offset, limit := 0, 5000

	for {
		msgs, hasNext, err := r.Read(chatPath, offset, limit)
		if err != nil {
			return nil, err
		}

		for _, m := range msgs {
			ts := time.Unix(int64(m["Date"].(float64)), 0)
			if !ts.Before(start) && ts.Before(end) {
				keep = append(keep, m)
			}
			if ts.After(end) {
				return keep, nil
			}
		}

		if !hasNext {
			return keep, nil
		}
		offset += limit
	}
}

type yearStats struct {
	totalMessages      int
	participantMsgs    map[int64]int
	participantChars   map[int64]int
	participantWords   map[int64]int
	participantEmoji   map[int64]int
	linkCount          map[int64]int
	forwardCount       map[int64]int
	instagramLinkCount map[int64]int
	youtubeLinkCount   map[int64]int
	loveEmojiCount     map[int64]int
	reactionsReceived  map[int64]int
	reactionsSent      map[int64]int
	emojiCounts        map[string]int
	wordCounts         map[string]int
	joiners            map[int64]joinLeaveInfo
	leavers            map[int64]joinLeaveInfo
	monthCount         map[time.Month]int
	dayCount           map[time.Time]int
	weekdayCount       map[time.Weekday]int
	hourCount          map[int]int
	firstMonthMsg      map[time.Month]chatMsg
	firstDayMsg        map[time.Time]chatMsg
	messageReactions   map[string]msgReaction
	longest            []longMessage
}

type joinLeaveInfo struct {
	date   time.Time
	chatID int64
	msgID  int32
}

type chatMsg struct {
	chatID int64
	msgID  int32
}

func newYearStats() *yearStats {
	return &yearStats{
		participantMsgs:    make(map[int64]int),
		participantChars:   make(map[int64]int),
		participantWords:   make(map[int64]int),
		participantEmoji:   make(map[int64]int),
		linkCount:          make(map[int64]int),
		forwardCount:       make(map[int64]int),
		instagramLinkCount: make(map[int64]int),
		youtubeLinkCount:   make(map[int64]int),
		loveEmojiCount:     make(map[int64]int),
		reactionsReceived:  make(map[int64]int),
		reactionsSent:      make(map[int64]int),
		emojiCounts:        make(map[string]int),
		wordCounts:         make(map[string]int),
		joiners:            make(map[int64]joinLeaveInfo),
		leavers:            make(map[int64]joinLeaveInfo),
		monthCount:         make(map[time.Month]int),
		dayCount:           make(map[time.Time]int),
		weekdayCount:       make(map[time.Weekday]int),
		hourCount:          make(map[int]int),
		firstMonthMsg:      make(map[time.Month]chatMsg),
		firstDayMsg:        make(map[time.Time]chatMsg),
		messageReactions:   make(map[string]msgReaction),
		longest:            make([]longMessage, 0, maxLongestMessages),
	}
}

func (s *yearStats) addMessage(chatID int64, msg map[string]any) {
	s.totalMessages++
	date := time.Unix(int64(msg["Date"].(float64)), 0)
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	month := date.Month()
	s.monthCount[month]++
	s.dayCount[day]++
	s.weekdayCount[date.Weekday()]++
	s.hourCount[date.Hour()]++

	// Track first message of each month
	if _, exists := s.firstMonthMsg[month]; !exists {
		if msgID, ok := msg["ID"].(float64); ok {
			s.firstMonthMsg[month] = chatMsg{chatID: chatID, msgID: int32(msgID)}
		}
	}

	if _, exists := s.firstDayMsg[day]; !exists {
		if msgID, ok := msg["ID"].(float64); ok {
			s.firstDayMsg[day] = chatMsg{chatID: chatID, msgID: int32(msgID)}
		}
	}

	if userID, ok := extractUserID(msg); ok {
		s.participantMsgs[userID]++
		if text, ok := msg["Message"].(string); ok {
			charLen := len([]rune(text))
			s.participantChars[userID] += charLen
			s.participantWords[userID] += len(strings.Fields(text))
			s.participantEmoji[userID] += countEmojis(text)
			for _, e := range emojiRegexp.FindAllString(text, -1) {
				s.emojiCounts[e]++
				if isLoveEmoji(e) {
					s.loveEmojiCount[userID]++
				}
			}
			s.linkCount[userID] += countLinks(text)
			// Count Instagram and YouTube links
			for _, domain := range instagramDomains {
				s.instagramLinkCount[userID] += countURLs(text, domain)
			}
			for _, domain := range youtubeDomains {
				s.youtubeLinkCount[userID] += countURLs(text, domain)
			}
			// Track top longest messages, excluding reposts forwarded from channels
			if !isForwardFromChannel(msg) {
				msgID := int32(msg["ID"].(float64))
				s.considerLongest(longMessage{userID: userID, chatID: chatID, date: date, msgID: msgID, chars: charLen, text: text}, maxLongestMessages)
			}
			for _, w := range splitWords(text) {
				lw := strings.ToLower(w)
				if _, stop := stopWords[lw]; stop {
					continue
				}
				if len([]rune(lw)) < minWordLength {
					continue
				}
				s.wordCounts[lw]++
			}
		}

		// Count forwards
		if fwd, ok := msg["FwdFrom"].(map[string]any); ok && len(fwd) > 0 {
			s.forwardCount[userID]++
		}
	}

	// Count reactions on messages
	msgAuthor, msgAuthorOk := extractUserID(msg)
	if reactionsData, ok := msg["Reactions"]; ok && reactionsData != nil {
		if reactionsMap, ok := reactionsData.(map[string]any); ok {
			var reactionCount int
			// Sum all reactions (Results can contain counts per reaction type)
			if results, ok := reactionsMap["Results"].([]any); ok {
				for _, r := range results {
					if rm, ok := r.(map[string]any); ok {
						if c, ok := rm["Count"].(float64); ok {
							reactionCount += int(c)
							continue
						}
					}
					reactionCount++
				}
			}
			// Extract RecentReactions array which contains who reacted
			if recentReactions, ok := reactionsMap["RecentReactions"].([]any); ok {
				for _, rxnData := range recentReactions {
					if rxnMap, ok := rxnData.(map[string]any); ok {
						// Extract the user ID from PeerID
						var senderID int64
						if peerID, ok := rxnMap["PeerID"].(map[string]any); ok {
							// PeerID can have UserID, ChannelID, etc.
							if userID, ok := peerID["UserID"]; ok {
								if id, ok := parseID(userID); ok {
									senderID = id
								}
							}
						}

						if senderID != 0 {
							s.reactionsSent[senderID]++
						}
					}
				}
				if reactionCount == 0 {
					// Fallback to recent reactions count if Results absent
					if recentReactions, ok := reactionsMap["RecentReactions"].([]any); ok {
						reactionCount = len(recentReactions)
					}
				}
				if reactionCount > 0 && msgAuthorOk {
					s.reactionsReceived[msgAuthor] += reactionCount
				}
				// Track message reactions for top reacted messages
				if reactionCount > 0 && msgAuthorOk {
					msgKey := fmt.Sprintf("%d:%d", chatID, msg["ID"])
					msgText := ""
					if text, ok := msg["Message"].(string); ok {
						msgText = text
					}
					strippedImg := extractStrippedImageBytes(msg)
					s.messageReactions[msgKey] = msgReaction{
						chatID:            chatID,
						msgID:             int32(msg["ID"].(float64)),
						userID:            msgAuthor,
						text:              msgText,
						date:              date,
						reactionCount:     reactionCount,
						strippedImageData: strippedImg,
					}
				}
			}
		}
	}

	action, ok := msg["Action"].(map[string]any)
	if ok {
		joins, leaves := extractActionUserIDs(action)
		msgID := int32(0)
		if mid, ok := msg["ID"].(float64); ok {
			msgID = int32(mid)
		}
		for _, id := range joins {
			if _, exists := s.joiners[id]; !exists {
				s.joiners[id] = joinLeaveInfo{date: date, chatID: chatID, msgID: msgID}
			}
		}
		for _, id := range leaves {
			if _, exists := s.leavers[id]; !exists {
				s.leavers[id] = joinLeaveInfo{date: date, chatID: chatID, msgID: msgID}
			}
		}
	}
}

func (s *yearStats) participantCount() int {
	return len(s.participantMsgs)
}

func (s *yearStats) mostActiveMonth() (time.Month, int) {
	maxMonth := time.Month(0)
	maxCount := 0
	for m, c := range s.monthCount {
		if c > maxCount || (c == maxCount && (maxMonth == 0 || m < maxMonth)) {
			maxMonth = m
			maxCount = c
		}
	}
	return maxMonth, maxCount
}

func (s *yearStats) leastActiveMonth() (time.Month, int) {
	if len(s.monthCount) == 0 {
		return time.Month(0), 0
	}
	minMonth := time.Month(0)
	minCount := -1
	for m, c := range s.monthCount {
		if minCount == -1 || c < minCount || (c == minCount && (minMonth == 0 || m < minMonth)) {
			minMonth = m
			minCount = c
		}
	}
	return minMonth, minCount
}

func (s *yearStats) mostActiveDay() (time.Time, int) {
	var maxDay time.Time
	maxCount := 0
	for d, c := range s.dayCount {
		if c > maxCount || (c == maxCount && (maxDay.IsZero() || d.Before(maxDay))) {
			maxDay = d
			maxCount = c
		}
	}
	return maxDay, maxCount
}

func (s *yearStats) leastActiveDay() (time.Time, int) {
	if len(s.dayCount) == 0 {
		return time.Time{}, 0
	}
	var minDay time.Time
	minCount := -1
	for d, c := range s.dayCount {
		if minCount == -1 || c < minCount || (c == minCount && (minDay.IsZero() || d.Before(minDay))) {
			minDay = d
			minCount = c
		}
	}
	return minDay, minCount
}

func (s *yearStats) firstMessageOfMonth(month time.Month) (chatID int64, msgID int32, found bool) {
	if info, exists := s.firstMonthMsg[month]; exists {
		return info.chatID, info.msgID, true
	}
	return 0, 0, false
}

func (s *yearStats) firstMessageOfDay(day time.Time) (chatID int64, msgID int32, found bool) {
	if info, exists := s.firstDayMsg[day]; exists {
		return info.chatID, info.msgID, true
	}
	return 0, 0, false
}

func (s *yearStats) mostActiveWeekday() (time.Weekday, int) {
	maxDay := time.Weekday(0)
	maxCount := 0
	for d, c := range s.weekdayCount {
		if c > maxCount || (c == maxCount && d < maxDay) {
			maxDay = d
			maxCount = c
		}
	}
	return maxDay, maxCount
}

func (s *yearStats) peakHour() (maxHour int, maxCount int) {
	maxHour = -1
	for h, c := range s.hourCount {
		if c > maxCount || (c == maxCount && (maxHour == -1 || h < maxHour)) {
			maxHour = h
			maxCount = c
		}
	}
	return maxHour, maxCount
}

func (s *yearStats) medianMsgsPerPerson() float64 {
	if len(s.participantMsgs) == 0 {
		return 0
	}
	counts := slices.Sorted(maps.Values(s.participantMsgs))
	mid := len(counts) / 2
	if len(counts)%2 == 1 {
		return float64(counts[mid])
	}
	return float64(counts[mid-1]+counts[mid]) / 2
}

func (s *yearStats) namesWithDates(reader *ChatCachedReader[UserData], ids map[int64]joinLeaveInfo) []string {
	res := make([]string, 0, len(ids))
	for id, info := range ids {
		name := formatUserName(reader, id)
		link := formatUserLink(reader, id, name)
		msgLink := formatMessageLink(info.chatID, info.msgID)
		d := info.date.UTC()
		res = append(res, fmt.Sprintf("%s ([%d %s](%s))", link, d.Day(), monthsUA[d.Month()], msgLink))
	}
	return slices.Sorted(slices.Values(res))
}

func (s *yearStats) reactionReceivedLeaderboard(reader *ChatCachedReader[UserData], limit int) []string {
	return s.buildReactionLeaderboard(reader, s.reactionsReceived, limit)
}

func (s *yearStats) reactionSentLeaderboard(reader *ChatCachedReader[UserData], limit int) []string {
	return s.buildReactionLeaderboard(reader, s.reactionsSent, limit)
}

func (s *yearStats) buildReactionLeaderboard(reader *ChatCachedReader[UserData], reactions map[int64]int, limit int) []string {
	if len(reactions) == 0 {
		return nil
	}

	type entry struct {
		id    int64
		name  string
		count int
	}

	entries := make([]entry, 0, len(reactions))
	for id, count := range reactions {
		if count >= 3 {
			name := formatUserName(reader, id)
			entries = append(entries, entry{id: id, name: name, count: count})
		}
	}

	slices.SortFunc(entries, func(a, b entry) int {
		if a.count != b.count {
			return cmp.Compare(b.count, a.count)
		}
		return cmp.Compare(a.name, b.name)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	res := make([]string, 0, len(entries))
	for _, e := range entries {
		nameLink := formatUserLink(reader, e.id, e.name)
		res = append(res, fmt.Sprintf("%s — %d реакцій", nameLink, e.count))
	}
	return res
}

func (s *yearStats) leaderboard(reader *ChatCachedReader[UserData], limit int) []string {
	if s.totalMessages == 0 {
		return nil
	}

	type entry struct {
		id      int64
		name    string
		count   int
		chars   int
		percent float64
	}

	entries := make([]entry, 0, len(s.participantMsgs))
	for id, count := range s.participantMsgs {
		name := formatUserName(reader, id)
		chars := s.participantChars[id]
		percent := float64(count) * 100 / float64(s.totalMessages)
		entries = append(entries, entry{id: id, name: name, count: count, chars: chars, percent: percent})
	}

	slices.SortFunc(entries, func(a, b entry) int {
		if a.count != b.count {
			return cmp.Compare(b.count, a.count)
		}
		return cmp.Compare(a.name, b.name)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	res := make([]string, 0, len(entries))
	for _, e := range entries {
		avgChars := 0.0
		if e.count > 0 {
			avgChars = float64(e.chars) / float64(e.count)
		}
		nameLink := formatUserLink(reader, e.id, e.name)
		res = append(res, fmt.Sprintf("%s — %d пов. (%.1f%%), середн. %.1f симв./повід.", nameLink, e.count, e.percent, avgChars))
	}
	return res
}

func (s *yearStats) topEmojis(limit int) (headerEmoji string, tokens []string) {
	if len(s.emojiCounts) == 0 {
		return "", nil
	}
	type pair struct {
		emoji string
		count int
	}
	arr := make([]pair, 0, len(s.emojiCounts))
	for e, c := range s.emojiCounts {
		arr = append(arr, pair{emoji: e, count: c})
	}
	slices.SortFunc(arr, func(a, b pair) int {
		if a.count != b.count {
			return cmp.Compare(b.count, a.count)
		}
		return cmp.Compare(a.emoji, b.emoji)
	})
	if limit > 0 && len(arr) > limit {
		arr = arr[:limit]
	}
	tokens = make([]string, len(arr))
	for i, p := range arr {
		tokens[i] = fmt.Sprintf("%s×%d", p.emoji, p.count)
	}
	headerEmoji = arr[0].emoji
	return headerEmoji, tokens
}

func (s *yearStats) funAwards(reader *ChatCachedReader[UserData]) []string {
	if len(s.participantMsgs) == 0 {
		return nil
	}

	maxMsgID, maxMsgCount := topByIntMap(s.participantMsgs, false)
	maxWordsID, maxWordsCount := topByIntMap(s.participantWords, false)
	maxEmojiID, maxEmojiCount := topByIntMap(s.participantEmoji, false)
	minMsgID, minMsgCount := topByIntMap(s.participantMsgs, true)
	maxLinksID, maxLinksCount := topByIntMap(s.linkCount, false)
	maxForwardID, maxForwardCount := topByIntMap(s.forwardCount, false)
	maxInstagramID, maxInstagramCount := topByIntMap(s.instagramLinkCount, false)
	maxYoutubeID, maxYoutubeCount := topByIntMap(s.youtubeLinkCount, false)
	maxLoveID, maxLoveCount := topByIntMap(s.loveEmojiCount, false)

	maxAvgID, maxAvg := topAvgChars(s.participantChars, s.participantMsgs)

	awards := []string{}
	if maxMsgID != 0 {
		awards = append(awards, fmt.Sprintf("🏆 MVP за найбільше повідомлень: %s — %d", formatUserLink(reader, maxMsgID, formatUserName(reader, maxMsgID)), maxMsgCount))
	}
	if maxWordsID != 0 {
		awards = append(awards, fmt.Sprintf("📝 Найбільше слів: %s — %d слів", formatUserLink(reader, maxWordsID, formatUserName(reader, maxWordsID)), maxWordsCount))
	}
	if maxEmojiID != 0 {
		awards = append(awards, fmt.Sprintf("😂 Емодзінатор (найбільше поставлених емодзі): %s — %d емодзі", formatUserLink(reader, maxEmojiID, formatUserName(reader, maxEmojiID)), maxEmojiCount))
	}
	if maxAvgID != 0 {
		awards = append(awards, fmt.Sprintf("📚 Есеїст (найдовші повідомлення в середньому): %s — %.1f симв./повід.", formatUserLink(reader, maxAvgID, formatUserName(reader, maxAvgID)), maxAvg))
	}
	if minMsgID != 0 {
		awards = append(awards, fmt.Sprintf("🕵️ Тихоня (найменше повідомлень): %s — %d", formatUserLink(reader, minMsgID, formatUserName(reader, minMsgID)), minMsgCount))
	}
	if maxLinksID != 0 {
		awards = append(awards, fmt.Sprintf("🔗 Пруфер (найбільше надісланих лінків): %s — %d", formatUserLink(reader, maxLinksID, formatUserName(reader, maxLinksID)), maxLinksCount))
	}
	if maxForwardID != 0 {
		awards = append(awards, fmt.Sprintf("📨 Форвардер: %s — %d пересилок", formatUserLink(reader, maxForwardID, formatUserName(reader, maxForwardID)), maxForwardCount))
	}
	if maxInstagramID != 0 {
		awards = append(awards, fmt.Sprintf("📷 Інстаграмер: %s — %d лінків на Instagram", formatUserLink(reader, maxInstagramID, formatUserName(reader, maxInstagramID)), maxInstagramCount))
	}
	if maxYoutubeID != 0 {
		awards = append(awards, fmt.Sprintf("🎬 Ютубер: %s — %d лінків на YouTube", formatUserLink(reader, maxYoutubeID, formatUserName(reader, maxYoutubeID)), maxYoutubeCount))
	}
	if maxLoveID != 0 {
		awards = append(awards, fmt.Sprintf("💕 Закоханий: %s — %d емодзі любові", formatUserLink(reader, maxLoveID, formatUserName(reader, maxLoveID)), maxLoveCount))
	}
	return awards
}

type longMessage struct {
	userID int64
	chatID int64
	date   time.Time
	msgID  int32
	chars  int
	text   string
}

func (s *yearStats) considerLongest(lm longMessage, limit int) {
	if lm.chars == 0 || strings.TrimSpace(lm.text) == "" {
		return
	}
	// If not yet filled, append
	if len(s.longest) < limit {
		s.longest = append(s.longest, lm)
		return
	}
	// Find current minimum
	minIdx := 0
	minVal := s.longest[0].chars
	for i := 1; i < len(s.longest); i++ {
		if s.longest[i].chars < minVal || (s.longest[i].chars == minVal && s.longest[i].date.After(s.longest[minIdx].date)) {
			minIdx = i
			minVal = s.longest[i].chars
		}
	}
	if lm.chars > minVal {
		s.longest[minIdx] = lm
	}
}

func (s *yearStats) getTopLongest(limit int) []longMessage {
	if len(s.longest) == 0 {
		return nil
	}
	longest := slices.Clone(s.longest)
	slices.SortFunc(longest, func(a, b longMessage) int {
		if a.chars != b.chars {
			return cmp.Compare(b.chars, a.chars)
		}
		if a.date.Before(b.date) {
			return -1
		}
		if a.date.After(b.date) {
			return 1
		}
		return 0
	})
	if limit > 0 && len(longest) > limit {
		longest = longest[:limit]
	}
	return longest
}

type msgReaction struct {
	chatID            int64
	msgID             int32
	userID            int64
	text              string
	date              time.Time
	reactionCount     int
	strippedImageData string
}

func (s *yearStats) getTopReactedMessages(limit int) []msgReaction {
	if len(s.messageReactions) == 0 {
		return nil
	}
	msgs := make([]msgReaction, 0, len(s.messageReactions))
	for _, data := range s.messageReactions {
		msgs = append(msgs, msgReaction{
			chatID:            data.chatID,
			msgID:             data.msgID,
			userID:            data.userID,
			text:              data.text,
			date:              data.date,
			reactionCount:     data.reactionCount,
			strippedImageData: data.strippedImageData,
		})
	}
	slices.SortFunc(msgs, func(a, b msgReaction) int {
		return cmp.Compare(b.reactionCount, a.reactionCount)
	})
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[:limit]
	}
	return msgs
}

func formatThousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b []byte
	rem := len(s) % 3
	if rem > 0 {
		b = append(b, s[:rem]...)
		b = append(b, ',')
	}
	for i := rem; i < len(s); i += 3 {
		b = append(b, s[i:i+3]...)
		if i+3 < len(s) {
			b = append(b, ',')
		}
	}
	return string(b)
}

func truncatePreview(text string, maxRunes int) string {
	rs := []rune(strings.TrimSpace(text))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes]) + "…"
}

func topByIntMap(m map[int64]int, wantMin bool) (int64, int) {
	var bestID int64
	bestVal := 0
	if wantMin {
		bestVal = -1
	}
	for id, val := range m {
		if wantMin {
			if val == 0 {
				continue
			}
			if bestVal == -1 || val < bestVal || (val == bestVal && id < bestID) {
				bestVal = val
				bestID = id
			}
		} else if val > bestVal || (val == bestVal && (bestID == 0 || id < bestID)) {
			bestVal = val
			bestID = id
		}
	}
	return bestID, bestVal
}

func topAvgChars(chars map[int64]int, msgs map[int64]int) (int64, float64) {
	var bestID int64
	bestAvg := 0.0
	for id, charCount := range chars {
		msgCount := msgs[id]
		if msgCount == 0 {
			continue
		}
		avg := float64(charCount) / float64(msgCount)
		if avg > bestAvg || (avg == bestAvg && (bestID == 0 || id < bestID)) {
			bestAvg = avg
			bestID = id
		}
	}
	return bestID, bestAvg
}

var emojiRegexp = regexp.MustCompile(`[\x{1F300}-\x{1F6FF}\x{1F900}-\x{1F9FF}\x{1FA70}-\x{1FAFF}\x{1F600}-\x{1F64F}][\x{1F3FB}-\x{1F3FF}\x{FE0E}\x{FE0F}\x{200D}]*`)

func countEmojis(text string) int {
	return len(emojiRegexp.FindAllString(text, -1))
}

func extractActionUserIDs(action map[string]any) (joins []int64, leaves []int64) {
	switch action["_"] {
	case "TL_messageActionChatAddUser", "TL_messageActionInviteToChannel":
		joins = append(joins, parseIDs(action["Users"])...)
		if id, ok := parseID(action["UserID"]); ok {
			joins = append(joins, id)
		}
	case "TL_messageActionChatJoinedByLink", "TL_messageActionChatJoinedByRequest":
		if id, ok := parseID(action["UserID"]); ok {
			joins = append(joins, id)
		}
	case "TL_messageActionChatDeleteUser":
		if id, ok := parseID(action["UserID"]); ok {
			leaves = append(leaves, id)
		}
	default:
	}
	return joins, leaves
}

func parseIDs(val any) []int64 {
	ids := make([]int64, 0)
	switch v := val.(type) {
	case []any:
		for _, item := range v {
			if id, ok := parseID(item); ok {
				ids = append(ids, id)
			}
		}
	case []int32:
		for _, item := range v {
			ids = append(ids, int64(item))
		}
	case []int64:
		ids = append(ids, v...)
	case []float64:
		for _, item := range v {
			ids = append(ids, int64(item))
		}
	}
	return ids
}

func parseID(val any) (int64, bool) {
	switch v := val.(type) {
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return id, true
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	}
	return 0, false
}

func countURLs(text string, domain string) int {
	count := 0
	for word := range strings.FieldsSeq(text) {
		if strings.Contains(word, domain) {
			count++
		}
	}
	return count
}

// countLinks returns count of words that look like URLs (basic http/https check).
func countLinks(text string) int {
	count := 0
	for word := range strings.FieldsSeq(text) {
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			count++
		}
	}
	return count
}

func extractUserID(msg map[string]any) (int64, bool) {
	from, ok := msg["FromID"].(map[string]any)
	if !ok {
		return 0, false
	}
	if id, ok := parseID(from["UserID"]); ok {
		return id, true
	}
	return 0, false
}

// Returns true if the message is a repost forwarded from a channel.
func isForwardFromChannel(msg map[string]any) bool {
	fwd, ok := msg["FwdFrom"].(map[string]any)
	if !ok {
		return false
	}
	fromID, ok := fwd["FromID"].(map[string]any)
	if !ok {
		return false
	}
	// Presence of ChannelID in FwdFrom.FromID indicates channel origin.
	if _, has := fromID["ChannelID"]; has {
		return true
	}
	return false
}

func formatUserName(reader *ChatCachedReader[UserData], id int64) string {
	user, err := reader.ReadOpt(id)
	if err == nil && user != nil {
		if user.Username != nil && *user.Username != "" {
			return "@" + *user.Username
		}
		name := strings.TrimSpace(derefOr(user.FirstName, "") + " " + derefOr(user.LastName, ""))
		if name != "" {
			return name
		}
		if user.IsDeleted {
			return "Deleted Account"
		}
	}
	return "user#" + strconv.FormatInt(id, 10)
}

// formatUserLink returns a markdown link to the user's Telegram profile if username is available.
func formatUserLink(reader *ChatCachedReader[UserData], id int64, displayName string) string {
	user, err := reader.ReadOpt(id)
	if err == nil && user != nil && user.Username != nil && *user.Username != "" {
		return fmt.Sprintf("[%s](https://t.me/%s)", displayName, *user.Username)
	}
	return displayName
}

// formatDateTimeUA formats a time as "15 гру 10:20" in Ukrainian.
func formatDateTimeUA(t time.Time) string {
	d := t.UTC()
	return fmt.Sprintf("%d %s %02d:%02d", d.Day(), monthsUA[d.Month()], d.Hour(), d.Minute())
}

// formatMessagePreview formats a message preview as quoted text.
func formatMessagePreview(text string, maxRunes int) string {
	preview := truncatePreview(text, maxRunes)
	return strings.ReplaceAll(preview, "\n", "\n> ")
}

// formatMessageLink returns a Telegram link to the message.
func formatMessageLink(chatID int64, msgID int32) string {
	// For private chats and groups, use the format: https://t.me/c/{abs(chat_id)}/{message_id}
	// Negative chat IDs indicate groups/channels; absolute value is used in the link
	absID := chatID
	if absID < 0 {
		absID = -absID
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", absID, msgID)
}

func (s *yearStats) topWords(limit int) []string {
	if len(s.wordCounts) == 0 {
		return nil
	}
	type pair struct {
		word  string
		count int
	}
	arr := make([]pair, 0, len(s.wordCounts))
	for w, c := range s.wordCounts {
		arr = append(arr, pair{word: w, count: c})
	}
	slices.SortFunc(arr, func(a, b pair) int {
		if a.count != b.count {
			return cmp.Compare(b.count, a.count)
		}
		return cmp.Compare(a.word, b.word)
	})
	if limit > 0 && len(arr) > limit {
		arr = arr[:limit]
	}
	res := make([]string, len(arr))
	for i, p := range arr {
		res[i] = fmt.Sprintf("%s — %d", p.word, p.count)
	}
	return res
}

// Minimal multilingual stopword set (EN, UK). Built programmatically to avoid duplicate keys.
var stopWords = func() map[string]struct{} {
	m := make(map[string]struct{})
	add := func(list []string) {
		for _, w := range list {
			m[w] = struct{}{}
		}
	}
	add([]string{ // English
		"the", "and", "a", "an", "to", "of", "in", "is", "it", "for",
		"on", "you", "your", "i", "we", "he", "she", "they", "them", "our", "my", "me",
		"that", "this", "these", "those", "with", "at", "as", "by", "from", "or", "not",
		"be", "are", "was", "were", "have", "has", "had", "do", "does", "did", "so",
		"if", "but", "than", "then", "also", "can", "could", "would", "should", "will",
		"just", "out", "up", "down", "over", "under", "here", "there", "too", "very",
		"https", "com", "www", "instagram", "youtube", "igsh", "reel",
	})
	add([]string{ // Ukrainian
		"і", "та", "а", "але", "що", "це", "цей", "ця", "ці", "ті", "той", "такий",
		"як", "у", "в", "на", "до", "з", "зі", "за", "по", "над", "під", "для", "про",
		"не", "ж", "би", "й", "я", "ти", "ви", "ми", "він", "вона", "вони", "від",
		"щоб", "коли", "де", "тут", "там", "також", "може", "було", "є", "теж", "вже",
		"якщо", "хто", "тому", "через", "був", "була", "було", "буде",
		"мені", "дуже", "щось", "його", "десь", "хтось", "навіть", "після", "можна", "треба",
		"мене", "таке", "типу", "який", "тебе", "собі", "бути", "тоді", "чому", "поки", "такі",
		"собі", "тобі", "саме", "цього",
	})
	return m
}()

func splitWords(s string) []string {
	words := []string{}
	var b []rune
	flush := func() {
		if len(b) > 0 {
			words = append(words, string(b))
			b = b[:0]
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b = append(b, r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

// extractStrippedImageBytes extracts the base64-encoded stripped JPEG thumbnail from a message.
// Returns empty string if no stripped image is found.
func extractStrippedImageBytes(msg map[string]any) string {
	media, ok := msg["Media"].(map[string]any)
	if !ok {
		return ""
	}
	photo, ok := media["Photo"].(map[string]any)
	if !ok {
		return ""
	}
	sizes, ok := photo["Sizes"].([]any)
	if !ok {
		return ""
	}

	// Look for TL_photoStrippedSize which contains embedded thumbnail
	for _, sizeAny := range sizes {
		size, ok := sizeAny.(map[string]any)
		if !ok {
			continue
		}
		if size["_"] == "TL_photoStrippedSize" {
			if bytes, ok := size["Bytes"].(string); ok {
				return bytes
			}
		}
	}
	return ""
}

// formatStrippedImageDataURI converts a base64-encoded stripped JPEG to a data URI.
// Implements the format used by Telegram's embedded photo thumbnails.
// Based on Telegram Desktop's ExpandInlineBytes implementation.
// https://github.com/telegramdesktop/tdesktop/blob/1757dd856b84d23f83d4e562c94dde825f6eb40c/Telegram/SourceFiles/ui/image/image.cpp#L43
func formatStrippedImageDataURI(strippedB64 string) string {
	if strippedB64 == "" {
		return ""
	}

	// Decode base64
	strippedBytes, err := base64.StdEncoding.DecodeString(strippedB64)
	if err != nil {
		return ""
	}

	// Telegram's stripped format: first byte must be 0x01, minimum length 3
	if len(strippedBytes) < 3 || strippedBytes[0] != 0x01 {
		return ""
	}

	// JPEG header from Telegram Desktop source code
	jpegHeader := []byte{
		0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49,
		0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x28, 0x1c,
		0x1e, 0x23, 0x1e, 0x19, 0x28, 0x23, 0x21, 0x23, 0x2d, 0x2b, 0x28, 0x30, 0x3c, 0x64, 0x41, 0x3c, 0x37, 0x37,
		0x3c, 0x7b, 0x58, 0x5d, 0x49, 0x64, 0x91, 0x80, 0x99, 0x96, 0x8f, 0x80, 0x8c, 0x8a, 0xa0, 0xb4, 0xe6, 0xc3,
		0xa0, 0xaa, 0xda, 0xad, 0x8a, 0x8c, 0xc8, 0xff, 0xcb, 0xda, 0xee, 0xf5, 0xff, 0xff, 0xff, 0x9b, 0xc1, 0xff,
		0xff, 0xff, 0xfa, 0xff, 0xe6, 0xfd, 0xff, 0xf8, 0xff, 0xdb, 0x00, 0x43, 0x01, 0x2b, 0x2d, 0x2d, 0x3c, 0x35,
		0x3c, 0x76, 0x41, 0x41, 0x76, 0xf8, 0xa5, 0x8c, 0xa5, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8,
		0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8,
		0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xf8,
		0xf8, 0xf8, 0xf8, 0xf8, 0xf8, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x00, 0x00, 0x00, 0x00, 0x03, 0x01, 0x22, 0x00,
		0x02, 0x11, 0x01, 0x03, 0x11, 0x01, 0xff, 0xc4, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01,
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0xff, 0xc4, 0x00, 0xb5, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05, 0x05,
		0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06,
		0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52,
		0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53,
		0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75,
		0x76, 0x77, 0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96,
		0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
		0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6,
		0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4,
		0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xc4, 0x00, 0x1f, 0x01, 0x00, 0x03, 0x01, 0x01, 0x01, 0x01, 0x01,
		0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0xff, 0xc4, 0x00, 0xb5, 0x11, 0x00, 0x02, 0x01, 0x02, 0x04, 0x04, 0x03, 0x04, 0x07, 0x05,
		0x04, 0x04, 0x00, 0x01, 0x02, 0x77, 0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21, 0x31, 0x06, 0x12, 0x41,
		0x51, 0x07, 0x61, 0x71, 0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91, 0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33,
		0x52, 0xf0, 0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34, 0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26,
		0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a,
		0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74,
		0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94,
		0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf2, 0xf3, 0xf4,
		0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x00, 0x0c, 0x03, 0x01, 0x00, 0x02, 0x11, 0x03, 0x11, 0x00,
		0x3f, 0x00,
	}
	jpegFooter := []byte{0xff, 0xd9}

	// Create JPEG with dimensions from bytes[1] and bytes[2] patched into positions 164 and 166
	fullJPEG := make([]byte, 0, len(jpegHeader)+len(strippedBytes)-3+len(jpegFooter))
	fullJPEG = append(fullJPEG, jpegHeader...)

	// Patch height (byte 1) at position 164 and width (byte 2) at position 166
	if len(fullJPEG) > 166 {
		fullJPEG[164] = strippedBytes[1]
		fullJPEG[166] = strippedBytes[2]
	}

	// Append the actual image data (starting from byte 3)
	fullJPEG = append(fullJPEG, strippedBytes[3:]...)
	fullJPEG = append(fullJPEG, jpegFooter...)

	// Convert to base64 and create data URI
	fullB64 := base64.StdEncoding.EncodeToString(fullJPEG)
	return "data:image/jpeg;base64," + fullB64
}

// isLoveEmoji checks if an emoji is a love/heart emoji
func isLoveEmoji(emoji string) bool {
	loveEmojis := map[string]bool{
		"❤️": true,
		"❤":  true,
		"♥️": true,
		"♥":  true,
		"💕":  true,
		"💖":  true,
		"💗":  true,
		"💘":  true,
		"💙":  true,
		"💚":  true,
		"💛":  true,
		"💜":  true,
		"🖤":  true,
		"🤍":  true,
		"🤎":  true,
		"❣️": true,
		"❣":  true,
		"💞":  true,
		"💓":  true,
		"💟":  true,
		"💝":  true,
		"🧡":  true,
		"💌":  true,
	}
	return loveEmojis[emoji]
}
