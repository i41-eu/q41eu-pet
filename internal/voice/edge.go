// Package voice turns the pet's lines into speech for the robot's speaker: Microsoft
// Edge's read-aloud voices (the protocol the edge-tts project uses), made a bit more
// robot and kid by ffmpeg, cached on disk; espeak-ng when Edge cannot be reached.
package voice

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Edge's read-aloud service, as used by the Edge browser (see github.com/rany2/edge-tts).
const (
	edgeURL        = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"
	edgeToken      = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	edgeChromium   = "143.0.3650.75"
	edgeMajor      = "143"
	edgeUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + edgeMajor + ".0.0.0 Safari/537.36 Edg/" + edgeMajor + ".0.0.0"
	edgeOrigin     = "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold"
	winEpochOffset = 11644473600 // seconds from 1601-01-01 to 1970-01-01
)

// EdgeVoice is a voice and its prosody, e.g. {"cs-CZ-AntoninNeural", "+25Hz", "+8%"}.
type EdgeVoice struct {
	Name, Pitch, Rate string
}

// secMSGEC is the token Edge sends: SHA-256 of the Windows file time (rounded down to
// 5 minutes, in 100 ns units) followed by the client token, upper-case hex.
func secMSGEC(now time.Time) string {
	secs := now.Unix() + winEpochOffset
	secs -= secs % 300
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d%s", secs*10_000_000, edgeToken)))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func jsDate(t time.Time) string {
	return t.UTC().Format("Mon Jan 02 2006 15:04:05") + " GMT+0000 (Coordinated Universal Time)"
}

// Edge returns the text spoken by the voice, as MP3 (24 kHz mono).
func Edge(ctx context.Context, v EdgeVoice, text string) ([]byte, error) {
	url := fmt.Sprintf("%s?TrustedClientToken=%s&ConnectionId=%s&Sec-MS-GEC=%s&Sec-MS-GEC-Version=1-%s",
		edgeURL, edgeToken, randomHex(16), secMSGEC(time.Now()), edgeChromium)
	h := http.Header{}
	h.Set("User-Agent", edgeUserAgent)
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Pragma", "no-cache")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", edgeOrigin)
	h.Set("Cookie", "muid="+strings.ToUpper(randomHex(16))+";")
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, EnableCompression: true}
	ws, resp, err := dialer.DialContext(ctx, url, h)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("edge: %s: %w", resp.Status, err)
		}
		return nil, fmt.Errorf("edge: %w", err)
	}
	defer ws.Close()
	if deadline, ok := ctx.Deadline(); ok {
		ws.SetReadDeadline(deadline)
	}

	now := jsDate(time.Now())
	config := "X-Timestamp:" + now + "\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n" +
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":"false","wordBoundaryEnabled":"false"},` +
		`"outputFormat":"audio-24khz-48kbitrate-mono-mp3"}}}}` + "\r\n"
	var escaped bytes.Buffer
	xml.EscapeText(&escaped, []byte(text))
	ssml := "<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'>" +
		"<voice name='" + v.Name + "'><prosody pitch='" + v.Pitch + "' rate='" + v.Rate + "' volume='+0%'>" +
		escaped.String() + "</prosody></voice></speak>"
	request := "X-RequestId:" + randomHex(16) + "\r\nContent-Type:application/ssml+xml\r\nX-Timestamp:" + now +
		"Z\r\nPath:ssml\r\n\r\n" + ssml
	if err := ws.WriteMessage(websocket.TextMessage, []byte(config)); err != nil {
		return nil, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
		return nil, err
	}

	var audio bytes.Buffer
	for {
		kind, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("edge: %w", err)
		}
		switch kind {
		case websocket.TextMessage:
			if bytes.Contains(msg, []byte("Path:turn.end")) {
				if audio.Len() == 0 {
					return nil, errors.New("edge: no audio")
				}
				return audio.Bytes(), nil
			}
		case websocket.BinaryMessage:
			// Two bytes of header length (big-endian), the headers, then the audio.
			if len(msg) < 2 {
				continue
			}
			hl := int(binary.BigEndian.Uint16(msg))
			if 2+hl > len(msg) || !bytes.Contains(msg[2:2+hl], []byte("Path:audio")) {
				continue
			}
			audio.Write(msg[2+hl:])
		}
	}
}
