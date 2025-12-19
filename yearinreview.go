package main

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ansel1/merry/v2"
)

func runYearInReview(saver *JSONFilesHistorySaver, year int, chatID int64) (string, error) {
	chatEntries, err := saver.ReadSavedChatsList()
	if err != nil {
		return "", merry.Wrap(err)
	}

	userReader := NewChatSyncReader[UserData](saver.usersFPath())
	chatReader := NewChatSyncReader[ChatData](saver.chatsFPath())

	if err := userReader.UpdateOffsets(); err != nil {
		return "", merry.Wrap(err)
	}
	if err := chatReader.UpdateOffsets(); err != nil {
		return "", merry.Wrap(err)
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
			return "", merry.Wrap(err)
		}
		if chatID != 0 {
			headerTitle = title
		}

		msgs, err := messagesInYear(chatsMsgReader, chatEntry.FPath, year)
		if err != nil {
			return "", merry.Wrap(err)
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
	weekday, weekdayCount := stats.mostActiveWeekday()
	hour, hourCount := stats.peakHour()
	median := stats.medianMsgsPerPerson()
	joinedNames := stats.names(userCache, stats.joiners)
	leavedNames := stats.names(userCache, stats.leavers)
	leaderboard := stats.leaderboard(userCache, 10)
	awards := stats.funAwards(userCache)

	monthLabel := "n/a"
	if monthCount > 0 {
		monthLabel = month.String()
	}
	weekdayLabel := "n/a"
	if weekdayCount > 0 {
		weekdayLabel = weekday.String()
	}
	hourLabel := "n/a"
	if hour >= 0 {
		hourLabel = fmt.Sprintf("%02d:00", hour)
	}

	if chatID != 0 {
		if headerTitle != "" {
			fmt.Fprintf(&buf, "# Year in Review — %d for %s (#%d)\n\n", year, headerTitle, chatID)
		} else {
			fmt.Fprintf(&buf, "# Year in Review — %d for chat #%d\n\n", year, chatID)
		}
	} else {
		fmt.Fprintf(&buf, "# Year in Review — %d\n\n", year)
	}

	fmt.Fprint(&buf, "## 📊 Highlights\n\n")
	fmt.Fprintf(&buf, "- **Total messages:** %d\n", total)
	fmt.Fprintf(&buf, "- **Participants:** %d\n", participants)
	if len(joinedNames) > 0 {
		fmt.Fprintf(&buf, "- **Joined users (%d):** %s\n", len(joinedNames), strings.Join(joinedNames, ", "))
	}
	if len(leavedNames) > 0 {
		fmt.Fprintf(&buf, "- **Left users (%d):** %s\n", len(leavedNames), strings.Join(leavedNames, ", "))
	}
	fmt.Fprintf(&buf, "- **Most active month:** %s (%d msgs)\n", monthLabel, monthCount)
	fmt.Fprintf(&buf, "- **Most active weekday:** %s (%d msgs)\n", weekdayLabel, weekdayCount)
	fmt.Fprintf(&buf, "- **Peak hour:** %s (%d msgs)\n", hourLabel, hourCount)
	fmt.Fprintf(&buf, "- **Median msgs/person:** %.1f\n", median)
	fmt.Fprint(&buf, "\n")

	if len(leaderboard) > 0 {
		fmt.Fprintf(&buf, "## 🏅 Leaderboard (messages)\n\n")
		for i, line := range leaderboard {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if len(awards) > 0 {
		fmt.Fprintf(&buf, "## 🎉 Fun Awards\n\n")
		for _, line := range awards {
			fmt.Fprintf(&buf, "- %s\n", line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if headerEmoji, tokens := stats.topEmojis(10); len(tokens) > 0 {
		fmt.Fprintf(&buf, "## 😊 Top emojis %s\n\n", headerEmoji)
		fmt.Fprintf(&buf, "`%s`\n\n", strings.Join(tokens, " "))
	}

	if topWords := stats.topWords(20); len(topWords) > 0 {
		fmt.Fprint(&buf, "## 📝 Top words (excluding common stopwords)\n\n")
		for _, line := range topWords {
			fmt.Fprintf(&buf, "- %s\n", line)
		}
		fmt.Fprint(&buf, "\n")
	}

	if longest := stats.getTopLongest(5); len(longest) > 0 {
		fmt.Fprint(&buf, "## 📜 Longest messages (by character count)\n\n")
		for _, lm := range longest {
			name := formatUserName(userCache, lm.userID)
			nameLink := formatUserLink(userCache, lm.userID, name)
			msgLink := formatMessageLink(lm.chatID, lm.msgID)
			fmt.Fprintf(&buf, "**%s** _%s_ — %s chars — [view](%s)\n\n", nameLink, lm.date.UTC().Format("2006-01-02 15:04"), formatThousands(lm.chars), msgLink)
			preview := truncatePreview(lm.text, 280)
			fmt.Fprintf(&buf, "> %s\n\n", strings.ReplaceAll(preview, "\n", "\n> "))
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
			return nil, merry.Wrap(err)
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
	instagramLinkCount map[int64]int
	youtubeLinkCount   map[int64]int
	emojiCounts        map[string]int
	wordCounts         map[string]int
	joiners            map[int64]struct{}
	leavers            map[int64]struct{}
	monthCount         map[time.Month]int
	weekdayCount       map[time.Weekday]int
	hourCount          map[int]int
	longest            []longMessage
}

func newYearStats() *yearStats {
	return &yearStats{
		participantMsgs:    make(map[int64]int),
		participantChars:   make(map[int64]int),
		participantWords:   make(map[int64]int),
		participantEmoji:   make(map[int64]int),
		instagramLinkCount: make(map[int64]int),
		youtubeLinkCount:   make(map[int64]int),
		emojiCounts:        make(map[string]int),
		wordCounts:         make(map[string]int),
		joiners:            make(map[int64]struct{}),
		leavers:            make(map[int64]struct{}),
		monthCount:         make(map[time.Month]int),
		weekdayCount:       make(map[time.Weekday]int),
		hourCount:          make(map[int]int),
		longest:            make([]longMessage, 0, 5),
	}
}

func (s *yearStats) addMessage(chatID int64, msg map[string]any) {
	s.totalMessages++
	date := time.Unix(int64(msg["Date"].(float64)), 0)
	s.monthCount[date.Month()]++
	s.weekdayCount[date.Weekday()]++
	s.hourCount[date.Hour()]++

	if userID, ok := extractUserID(msg); ok {
		s.participantMsgs[userID]++
		if text, ok := msg["Message"].(string); ok {
			charLen := len([]rune(text))
			s.participantChars[userID] += charLen
			s.participantWords[userID] += len(strings.Fields(text))
			s.participantEmoji[userID] += countEmojis(text)
			for _, e := range emojiRegexp.FindAllString(text, -1) {
				s.emojiCounts[e]++
			}
			// Count Instagram and YouTube links
			s.instagramLinkCount[userID] += countURLs(text, "instagram.com") + countURLs(text, "ddinstagram.com")
			s.youtubeLinkCount[userID] += countURLs(text, "youtube.com") + countURLs(text, "youtu.be")
			// Track top longest messages, excluding reposts forwarded from channels
			if !isForwardFromChannel(msg) {
				msgID := int32(msg["ID"].(float64))
				s.considerLongest(longMessage{userID: userID, chatID: chatID, date: date, msgID: msgID, chars: charLen, text: text}, 5)
			}
			for _, w := range splitWords(text) {
				lw := strings.ToLower(w)
				if _, stop := stopWords[lw]; stop {
					continue
				}
				if len([]rune(lw)) < 3 {
					continue
				}
				s.wordCounts[lw]++
			}
		}
	}

	action, ok := msg["Action"].(map[string]any)
	if ok {
		joins, leaves := extractActionUserIDs(action)
		for _, id := range joins {
			s.joiners[id] = struct{}{}
		}
		for _, id := range leaves {
			s.leavers[id] = struct{}{}
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

func (s *yearStats) peakHour() (int, int) {
	maxHour := -1
	maxCount := 0
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

func (s *yearStats) names(reader *ChatCachedReader[UserData], ids map[int64]struct{}) []string {
	res := make([]string, 0, len(ids))
	for id := range ids {
		res = append(res, formatUserName(reader, id))
	}
	return slices.Sorted(slices.Values(res))
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
		res = append(res, fmt.Sprintf("%s - %d msgs (%.1f%%), avg %.1f chars/msg", nameLink, e.count, e.percent, avgChars))
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
	return
}

func (s *yearStats) funAwards(reader *ChatCachedReader[UserData]) []string {
	if len(s.participantMsgs) == 0 {
		return nil
	}

	maxMsgID, maxMsgCount := topByIntMap(s.participantMsgs, false)
	maxWordsID, maxWordsCount := topByIntMap(s.participantWords, false)
	maxEmojiID, maxEmojiCount := topByIntMap(s.participantEmoji, false)
	minMsgID, minMsgCount := topByIntMap(s.participantMsgs, true)
	maxInstagramID, maxInstagramCount := topByIntMap(s.instagramLinkCount, false)
	maxYoutubeID, maxYoutubeCount := topByIntMap(s.youtubeLinkCount, false)

	maxAvgID, maxAvg := topAvgChars(s.participantChars, s.participantMsgs)

	awards := []string{}
	if maxMsgID != 0 {
		awards = append(awards, fmt.Sprintf("🏆 MVP (most messages): %s — %d", formatUserLink(reader, maxMsgID, formatUserName(reader, maxMsgID)), maxMsgCount))
	}
	if maxWordsID != 0 {
		awards = append(awards, fmt.Sprintf("📝 Most words typed: %s — %d words", formatUserLink(reader, maxWordsID, formatUserName(reader, maxWordsID)), maxWordsCount))
	}
	if maxEmojiID != 0 {
		awards = append(awards, fmt.Sprintf("😂 Emoji machine: %s — %d emojis", formatUserLink(reader, maxEmojiID, formatUserName(reader, maxEmojiID)), maxEmojiCount))
	}
	if maxAvgID != 0 {
		awards = append(awards, fmt.Sprintf("📚 Essayist (longest avg message): %s — %.1f chars/msg", formatUserLink(reader, maxAvgID, formatUserName(reader, maxAvgID)), maxAvg))
	}
	if minMsgID != 0 {
		awards = append(awards, fmt.Sprintf("🕵️ Lurker (fewest messages): %s — %d", formatUserLink(reader, minMsgID, formatUserName(reader, minMsgID)), minMsgCount))
	}
	if maxInstagramID != 0 {
		awards = append(awards, fmt.Sprintf("📷 Instagrammer: %s — %d links", formatUserLink(reader, maxInstagramID, formatUserName(reader, maxInstagramID)), maxInstagramCount))
	}
	if maxYoutubeID != 0 {
		awards = append(awards, fmt.Sprintf("🎬 YouTuber: %s — %d links", formatUserLink(reader, maxYoutubeID, formatUserName(reader, maxYoutubeID)), maxYoutubeCount))
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
		} else {
			if val > bestVal || (val == bestVal && (bestID == 0 || id < bestID)) {
				bestVal = val
				bestID = id
			}
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
	}
	return
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
	for _, word := range strings.Fields(text) {
		if strings.Contains(word, domain) {
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
		"https", "com", "www",
	})
	add([]string{ // Ukrainian
		"і", "та", "а", "але", "що", "це", "цей", "ця", "ці", "ті", "той", "такий",
		"як", "у", "в", "на", "до", "з", "зі", "за", "по", "над", "під", "для", "про",
		"не", "ж", "би", "й", "я", "ти", "ви", "ми", "він", "вона", "вони", "від",
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
