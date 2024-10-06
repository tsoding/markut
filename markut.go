package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"errors"
	"time"
	"io/ioutil"
	"strconv"
	"sort"
)

func millisToTs(millis Millis) string {
	sign := ""
	if millis < 0 {
		sign = "-"
		millis = -millis
	}
	hh := millis / 1000 / 60 / 60
	mm := millis / 1000 / 60 % 60
	ss := millis / 1000 % 60
	ms := millis % 1000
	return fmt.Sprintf("%s%02d:%02d:%02d.%03d", sign, hh, mm, ss, ms)
}

func millisToSubRipTs(millis Millis) string {
	sign := ""
	if millis < 0 {
		sign = "-"
		millis = -millis
	}
	hh := millis / 1000 / 60 / 60
	mm := millis / 1000 / 60 % 60
	ss := millis / 1000 % 60
	ms := millis % 1000
	return fmt.Sprintf("%s%02d:%02d:%02d,%03d", sign, hh, mm, ss, ms)
}

type ChatMessage struct {
	TimeOffset Millis
	Text string
}

type Chunk struct {
	Start Millis
	End   Millis
	Loc Loc
	InputPath string
	ChatLog []ChatMessage
	Blur bool
	Unfinished bool
}

const ChunksFolder = "chunks"

func (chunk Chunk) Name() string {
	inputPath := strings.ReplaceAll(chunk.InputPath, "/", "_")
	sb := strings.Builder{}
	fmt.Fprintf(&sb, "%s/%s-%09d-%09d", ChunksFolder, inputPath, chunk.Start, chunk.End)
	if chunk.Blur {
		sb.WriteString("-blur")
	}
	sb.WriteString(".mp4")
	return sb.String()
}

func (chunk Chunk) Duration() Millis {
	return chunk.End - chunk.Start
}

type Chapter struct {
	Loc Loc
	Timestamp Millis
	Label string
}

const MinYouTubeChapterDuration Millis = 10*1000;

func (context *EvalContext) typeCheckArgs(loc Loc, signature ...TokenKind) (args []Token, err error) {
	if len(context.argsStack) < len(signature) {
		err = &DiagErr{
			Loc: loc,
			Err: fmt.Errorf("Expected %d arguments but got %d", len(signature), len(context.argsStack)),
		}
		return
	}

	for _, kind := range signature {
		n := len(context.argsStack)
		arg := context.argsStack[n-1]
		context.argsStack = context.argsStack[:n-1]
		if kind != arg.Kind {
			err = &DiagErr{
				Loc: arg.Loc,
				Err: fmt.Errorf("Expected %s but got %s", TokenKindName[kind], TokenKindName[arg.Kind]),
			}
			return
		}
		args = append(args, arg)
	}

	return
}

type Cut struct {
	chunk int
	pad Millis
}

type EvalContext struct {
	inputPath string
	outputPath string
	chatLog []ChatMessage
	chunks []Chunk
	chapters []Chapter
	cuts []Cut

	argsStack []Token
	chapStack []Chapter
	chapOffset Millis

	VideoCodec string
	VideoBitrate string
	AudioCodec string
	AudioBitrate string

	ExtraOutFlags []string
	ExtraInFlags []string
}

const (
	DefaultVideoCodec = "libx264"
	DefaultVideoBitrate = "4000k"
	DefaultAudioCodec = "aac"
	DefaultAudioBitrate = "300k"
)

func defaultContext() (context EvalContext) {
	// Default chunk transcoding parameters
	context.VideoCodec = DefaultVideoCodec
	context.VideoBitrate = DefaultVideoBitrate
	context.AudioCodec = DefaultAudioCodec
	context.AudioBitrate = DefaultAudioBitrate
	context.outputPath = "output.mp4"
	return
}

func (context EvalContext) PrintSummary() {
	fmt.Println("Cuts:")
	var fullLength Millis = 0
	var finishedLength Millis = 0
	var renderedLength Millis = 0
	for i, chunk := range context.chunks {
		if i < len(context.chunks) - 1 {
			fmt.Printf("%s: %s: %s\n", chunk.Loc, millisToTs(fullLength + chunk.Duration()), fmt.Sprintf("cut-%02d.mp4", i))
		}
		fullLength += chunk.Duration()
		if !chunk.Unfinished {
			finishedLength += chunk.Duration()
		}
		if _, err := os.Stat(chunk.Name()); err == nil {
			renderedLength += chunk.Duration()
		}
	}
	fmt.Println()
	fmt.Println("Chapters:")
	for _, chapter := range context.chapters {
		fmt.Printf("- %s - %s\n", millisToTs(chapter.Timestamp), chapter.Label)
	}
	fmt.Println()
	fmt.Printf("Chunks Count: %d\n", len(context.chunks))
	fmt.Printf("Cuts Count: %d\n", len(context.chunks) - 1)
	fmt.Println()
	fmt.Printf("Rendered Length: %s\n", millisToTs(renderedLength))
	fmt.Printf("Finished Length: %s\n", millisToTs(finishedLength))
	fmt.Printf("Full Length:     %s\n", millisToTs(fullLength))
}

func (context EvalContext) containsChunkWithName(filePath string) bool {
	for _, chunk := range(context.chunks) {
		if chunk.Name() == filePath {
			return true
		}
	}
	return false
}

// IMPORTANT! chatLog is assumed to be sorted by TimeOffset.
func sliceChatLog(chatLog []ChatMessage, start, end Millis) []ChatMessage {
	// TODO: use Binary Search for a speed up on big chat logs
	lower := 0
	for lower < len(chatLog) && chatLog[lower].TimeOffset < start {
		lower += 1
	}
	upper := lower;
	for upper < len(chatLog) && chatLog[upper].TimeOffset <= end {
		upper += 1
	}
	if lower < len(chatLog) {
		return chatLog[lower:upper]
	}
	return []ChatMessage{}
}

// IMPORTANT! chatLog is assumed to be sorted by TimeOffset.
func compressChatLog(chatLog []ChatMessage) []ChatMessage {
	result := []ChatMessage{}
	for i := range chatLog {
		if len(result) > 0 && result[len(result)-1].TimeOffset == chatLog[i].TimeOffset {
			result[len(result)-1].Text = result[len(result)-1].Text + "\n" + chatLog[i].Text
		} else {
			result = append(result, chatLog[i])
		}
	}
	return result
}

type Func struct {
	Description string
	Signature string
	Category string
	Run func(context *EvalContext, command string, token Token) bool
}

var funcs = map[string]Func{
	"chat": {
		Description: "Load a chat log file generated by https://www.twitchchatdownloader.com/$SPOILER$ which is going to be used by the subsequent `chunk` func calls to include certain messages into the subtitles generated by the `markut chat` subcommand. There could be only one loaded chat log at a time. Repeated calls to the `chat` func replace the currently loaded chat log with another one. The already defined chunks keep the copy of the logs that were loaded at the time of their definition.",
		Signature: "<path:String> --",
		Category: "Chat",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			path := args[0]
			context.chatLog, err = loadTwitchChatDownloaderCSVButParseManually(string(path.Text))
			if err != nil {
				fmt.Printf("%s: ERROR: could not load the chat logs: %s\n", path.Loc, err)
				return false
			}
			return true
		},
	},
	"chat_offset": {
		Description: "Offsets the timestamps of the currently loaded chat log$SPOILER$ by removing all the messages between `start` and `end` Timestamps",
		Category: "Chat",
		Signature: "<start:Timestamp> <end:Timestamp> --",
		Run: func(context *EvalContext, command string, token Token) bool {
			// // TODO: this check does not make any sense when there are several chat commands
			// if len(context.chunks) > 0  {
			// 	fmt.Printf("%s: ERROR: chat offset should be applied after `chat` commands but before any `chunks` commands. This is due to `chunk` commands making copies of the chat slices that are not affected by the consequent chat offsets\n", token.Loc);
			// 	return false;
			// }

			args, err := context.typeCheckArgs(token.Loc, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}

			start := args[1]
			end := args[0]

			if start.Timestamp < 0 {
				fmt.Printf("%s: ERROR: the start of the chat offset is negative %s\n", start.Loc, millisToTs(start.Timestamp));
				return false
			}

			if end.Timestamp < 0 {
				fmt.Printf("%s: ERROR: the end of the chat offset is negative %s\n", end.Loc, millisToTs(end.Timestamp));
				return false
			}

			if start.Timestamp > end.Timestamp {
				fmt.Printf("%s: ERROR: the end of the chat offset %s is earlier than its start %s\n", end.Loc, millisToTs(end.Timestamp), millisToTs(start.Timestamp));
				fmt.Printf("%s: NOTE: the start is located here\n", start.Loc);
				return false
			}

			chatLen := len(context.chatLog)
			if chatLen > 0 {
				last := context.chatLog[chatLen-1].TimeOffset
				before := sliceChatLog(context.chatLog, 0, start.Timestamp)
				after := sliceChatLog(context.chatLog, end.Timestamp, last)
				delta := end.Timestamp - start.Timestamp
				for i := range after {
					after[i].TimeOffset -= delta
				}
				context.chatLog = append(before, after...)
			}

			return true
		},
	},
	"no_chat": {
		Description: "Clears out the current loaded chat log$SPOILER$ as if nothing is loaded",
		Category: "Chat",
		Signature: "--",
		Run: func(context *EvalContext, command string, token Token) bool {
			context.chatLog = []ChatMessage{}
			return true
		},
	},
	"chunk": {
		Description: "Define a chunk$SPOILER$ between `start` and `end` timestamp for the current input defined by the `input` func",
		Category: "Chunk",
		Signature: "<start:Timestamp> <end:Timestamp> --",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}

			start := args[1]
			end := args[0]

			if start.Timestamp < 0 {
				fmt.Printf("%s: ERROR: the start of the chunk is negative %s\n", start.Loc, millisToTs(start.Timestamp));
				return false
			}

			if end.Timestamp < 0 {
				fmt.Printf("%s: ERROR: the end of the chunk is negative %s\n", end.Loc, millisToTs(end.Timestamp));
				return false
			}

			if start.Timestamp > end.Timestamp {
				fmt.Printf("%s: ERROR: the end of the chunk %s is earlier than its start %s\n", end.Loc, millisToTs(end.Timestamp), millisToTs(start.Timestamp));
				fmt.Printf("%s: NOTE: the start is located here\n", start.Loc);
				return false
			}

			chunk := Chunk{
				Loc: token.Loc,
				Start: start.Timestamp,
				End: end.Timestamp,
				InputPath: context.inputPath,
				ChatLog: sliceChatLog(context.chatLog, start.Timestamp, end.Timestamp),
			}

			context.chunks = append(context.chunks, chunk)

			for _, chapter := range context.chapStack {
				if chapter.Timestamp < chunk.Start || chunk.End < chapter.Timestamp {
					fmt.Printf("%s: ERROR: the timestamp %s of chapter \"%s\" is outside of the the current chunk\n", chapter.Loc, millisToTs(chapter.Timestamp), chapter.Label)
					fmt.Printf("%s: NOTE: which starts at %s\n", start.Loc, millisToTs(start.Timestamp))
					fmt.Printf("%s: NOTE: and ends at %s\n", end.Loc, millisToTs(end.Timestamp))
					return false
				}

				context.chapters = append(context.chapters, Chapter{
					Loc: chapter.Loc,
					Timestamp: chapter.Timestamp - chunk.Start + context.chapOffset,
					Label: chapter.Label,
				})
			}

			context.chapOffset += chunk.End - chunk.Start

			context.chapStack = []Chapter{}
			return true
		},

	},
	"blur": {
		Description: "Blur the last defined chunk$SPOILER$. Useful for bluring out sensitive information.",
		Signature: "--",
		Category: "Chunk",
		Run: func(context *EvalContext, command string, token Token) bool {
			if len(context.chunks) == 0 {
				fmt.Printf("%s: ERROR: no chunks defined for a blur\n", token.Loc)
				return false
			}
			context.chunks[len(context.chunks)-1].Blur = true
			return true
		},
	},
	"removed": {
		Description: "Remove the last defined chunk$SPOILER$. Useful for disabling a certain chunk, so you can reenable it later if needed.",
		Signature: "--",
		Category: "Chunk",
		Run: func(context *EvalContext, command string, token Token) bool {
			if len(context.chunks) == 0 {
				fmt.Printf("%s: ERROR: no chunks defined for removal\n", token.Loc)
				return false
			}
			context.chunks = context.chunks[:len(context.chunks)-1]
			return true
		},
	},
	"unfinished": {
		Description: "Mark the last defined chunk as unfinished$SPOILER$. This is used by the `markut watch` subcommand. `markut watch` does not render any unfinished chunks and keeps monitoring the MARKUT file until there is no unfinished chunks.",
		Signature: "--",
		Category: "Chunk",
		Run: func(context *EvalContext, command string, token Token) bool {
			if len(context.chunks) == 0 {
				fmt.Printf("%s: ERROR: no chunks defined for marking as unfinished\n", token.Loc)
				return false
			}
			context.chunks[len(context.chunks)-1].Unfinished = true
			return true
		},
	},
	"video_codec": {
		Description: "Set the value of the output video codec flag (-c:v). Default is \""+DefaultVideoCodec+"\".",
		Signature: "<codec:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			context.VideoCodec = string(args[0].Text)
			return true;
		},
	},
	"video_bitrate": {
		Description: "Set the value of the output video bitrate flag (-vb). Default is \""+DefaultVideoBitrate+"\".",
		Signature: "<bitrate:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			context.VideoBitrate = string(args[0].Text)
			return true;
		},
	},
	"audio_codec": {
		Description: "Set the value of the output audio codec flag (-c:a). Default is \""+DefaultAudioCodec+"\".",
		Signature: "<codec:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			context.AudioCodec = string(args[0].Text)
			return true;
		},
	},
	"audio_bitrate": {
		Description: "Set the value of the output audio bitrate flag (-ab). Default is \""+DefaultAudioBitrate+"\".",
		Signature: "<bitrate:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			context.AudioBitrate = string(args[0].Text)
			return true;
		},
	},
	"outf": {
		Description: "Append extra output flag",
		Signature: "<flag:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			outFlag := args[0]
			context.ExtraOutFlags = append(context.ExtraOutFlags, string(outFlag.Text))
			return true;
		},
	},
	"inf": {
		Description: "Append extra input flag",
		Signature: "<flag:String> --",
		Category: "FFmpeg Arguments",
		Run: func(context *EvalContext, command string, token Token) bool {
			args, err := context.typeCheckArgs(token.Loc, TokenString)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
				fmt.Printf("%s\n", err)
				return false
			}
			inFlag := args[0]
			context.ExtraInFlags = append(context.ExtraInFlags, string(inFlag.Text))
			return true;
		},
	},
}

// This function is compatible with the format https://www.twitchchatdownloader.com/ generates.
// It does not use encoding/csv because that website somehow generates unparsable garbage.
func loadTwitchChatDownloaderCSVButParseManually(path string) ([]ChatMessage, error) {
	chatLog := []ChatMessage{}
	f, err := os.Open(path);
	if err != nil {
		return chatLog, err
	}
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return chatLog, err
	}

	content := string(bytes)
	for i, line := range strings.Split(content, "\n") {
		if len(line) == 0 {
			break
		}
		pair := strings.SplitN(line, ",", 2)
		secs, err := strconv.Atoi(pair[0])
		if err != nil {
			return chatLog, fmt.Errorf("%s:%d: invalid timestamp: %w", path, i, err)
		}

		pair = strings.SplitN(pair[1], ",", 2)
		nickname := pair[0]

		pair = strings.SplitN(pair[1], ",", 2)
		text := pair[1]

		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			text = text[1:len(text)-1]
		}

		chatLog = append(chatLog, ChatMessage{
			TimeOffset: Millis(secs*1000),
			Text: fmt.Sprintf("[%s] %s", nickname, text),
		})
	}

	sort.Slice(chatLog, func(i, j int) bool {
		return chatLog[i].TimeOffset < chatLog[j].TimeOffset
	})

	return compressChatLog(chatLog), nil
}

func (context *EvalContext) evalMarkutFile(path string) bool {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("ERROR: could not read file %s: %s\n", path, err)
		return false
	}

	lexer := NewLexer(string(content), path)
	token := Token{}
	for {
		token, err = lexer.Next()
		if err != nil {
			fmt.Printf("%s\n", err)
			return false
		}

		if token.Kind == TokenEOF {
			break
		}

		var args []Token
		switch token.Kind {
		case TokenDash:
			args, err = context.typeCheckArgs(token.Loc, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for subtraction\n", token.Loc)
				fmt.Printf("%s\n", err);
				return false
			}
			context.argsStack = append(context.argsStack, Token{
				Loc: token.Loc,
				Kind: TokenTimestamp,
				Timestamp: args[1].Timestamp - args[0].Timestamp,
			})
		case TokenPlus:
			args, err = context.typeCheckArgs(token.Loc, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for addition\n", token.Loc)
				fmt.Printf("%s\n", err);
				return false
			}
			context.argsStack = append(context.argsStack, Token{
				Loc: token.Loc,
				Kind: TokenTimestamp,
				Timestamp: args[1].Timestamp + args[0].Timestamp,
			})
		case TokenString:
			fallthrough
		case TokenTimestamp:
			context.argsStack = append(context.argsStack, token)
		case TokenSymbol:
			command := string(token.Text)
			switch command {
			case "include":
				args, err = context.typeCheckArgs(token.Loc, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				path := args[0]
				if !context.evalMarkutFile(string(path.Text)) {
					return false
				}
			case "input":
				args, err = context.typeCheckArgs(token.Loc, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				path := args[0]
				if len(path.Text) == 0 {
					fmt.Printf("%s: ERROR: cannot set empty input path\n", path.Loc);
					return false
				}
				context.inputPath = string(path.Text)
			case "over":
				arity := 2
				if len(context.argsStack) < arity {
					fmt.Printf("%s: Expected %d arguments but got %d", token.Loc, arity, len(context.argsStack));
					return false;
				}
				n := len(context.argsStack)
				context.argsStack = append(context.argsStack, context.argsStack[n-2]);
			case "dup":
				arity := 1
				if len(context.argsStack) < arity {
					fmt.Printf("%s: Expected %d arguments but got %d", token.Loc, arity, len(context.argsStack));
					return false;
				}
				n := len(context.argsStack)
				// TODO: the location of the dupped value should be the location of the "dup" token
				context.argsStack = append(context.argsStack, context.argsStack[n-1])
			case "chapter":
				args, err = context.typeCheckArgs(token.Loc, TokenString, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				context.chapStack = append(context.chapStack, Chapter{
					Loc: args[1].Loc,
					Label: string(args[0].Text),
					Timestamp: args[1].Timestamp,
				})
			case "puts":
				args, err = context.typeCheckArgs(token.Loc, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				fmt.Printf("%s", string(args[0].Text));
			case "putd":
				args, err = context.typeCheckArgs(token.Loc, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				fmt.Printf("%d", int(args[0].Timestamp));
			case "putt":
				args, err = context.typeCheckArgs(token.Loc, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				fmt.Printf("%s", millisToTs(args[0].Timestamp));
			case "here":
				context.argsStack = append(context.argsStack, Token{
					Loc: token.Loc,
					Kind: TokenString,
					Text: []rune(token.Loc.String()),
				})
			case "chunk_location":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					return false
				}

				context.argsStack = append(context.argsStack, Token{
					Loc: token.Loc,
					Kind: TokenString,
					Text: []rune(context.chunks[n-1].Loc.String()),
				})
			case "chunk_number":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					return false
				}

				context.argsStack = append(context.argsStack, Token{
					Loc: token.Loc,
					Kind: TokenTimestamp,
					Timestamp: Millis(n-1),
				})
			case "chunk_duration":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					return false
				}

				context.argsStack = append(context.argsStack, Token{
					Loc: token.Loc,
					Kind: TokenTimestamp,
					Timestamp: context.chunks[n-1].Duration(),
				})
			case "cut":
				args, err = context.typeCheckArgs(token.Loc, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				pad := args[0]
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for a cut\n", token.Loc)
					return false
				}
				context.cuts = append(context.cuts, Cut{
					chunk: len(context.chunks) - 1,
					pad: pad.Timestamp,
				})
			default:
				f, ok := funcs[command];
				if !ok {
					fmt.Printf("%s: ERROR: Unknown command %s\n", token.Loc, command)
					return false
				}
				if !f.Run(context, command, token) {
					return false
				}
			}
		default:
			fmt.Printf("%s: ERROR: Unexpected token %s\n", token.Loc, TokenKindName[token.Kind]);
			return false
		}
	}

	return true
}

func (context *EvalContext) finishEval() bool {
	for i := 0; i + 1 < len(context.chapters); i += 1 {
		duration := context.chapters[i + 1].Timestamp - context.chapters[i].Timestamp;
		// TODO: angled brackets are not allowed on YouTube. Let's make `chapters` check for that too.
		if duration < MinYouTubeChapterDuration {
			fmt.Printf("%s: ERROR: the chapter \"%s\" has duration %s which is shorter than the minimal allowed YouTube chapter duration which is %s (See https://support.google.com/youtube/answer/9884579)\n", context.chapters[i].Loc, context.chapters[i].Label, millisToTs(duration), millisToTs(MinYouTubeChapterDuration));
			fmt.Printf("%s: NOTE: the chapter ends here\n", context.chapters[i + 1].Loc);
			return false;
		}
	}

	if len(context.argsStack) > 0 || len(context.chapStack) > 0 {
		for i := range context.argsStack {
			fmt.Printf("%s: ERROR: unused argument\n", context.argsStack[i].Loc)
		}
		for i := range context.chapStack {
			fmt.Printf("%s: ERROR: unused chapter\n", context.chapStack[i].Loc)
		}
		return false
	}

	return true
}

func ffmpegPathToBin() (ffmpegPath string) {
	ffmpegPath = "ffmpeg"
	ffmpegPrefix, ok := os.LookupEnv("FFMPEG_PREFIX")
	if ok {
		ffmpegPath = path.Join(ffmpegPrefix, "bin", "ffmpeg")
	}
	return
}

func logCmd(name string, args ...string) {
	chunks := []string{}
	chunks = append(chunks, name)
	for _, arg := range args {
		if strings.Contains(arg, " ") {
			// TODO: use proper shell escaping instead of just wrapping with double quotes
			chunks = append(chunks, "\""+arg+"\"")
		} else {
			chunks = append(chunks, arg)
		}
	}
	fmt.Printf("[CMD] %s\n", strings.Join(chunks, " "))
}

func millisToSecsForFFmpeg(millis Millis) string {
	return fmt.Sprintf("%d.%03d", millis/1000, millis%1000)
}

func ffmpegCutChunk(context EvalContext, chunk Chunk) error {
	_, err := os.Stat(chunk.Name())
	if err == nil {
		fmt.Printf("INFO: %s is already rendered\n", chunk.Name());
		return nil;
	}

	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	err = os.MkdirAll(ChunksFolder, 0755)
	if err != nil {
		return err
	}

	ffmpeg := ffmpegPathToBin()
	args := []string{}

	// We always rerender unfinished-chunk.mp4, because it might still
	// exist due to the rendering erroring out or canceling. It's a
	// temporary file that is copied and renamed to the chunks/ folder
	// after the rendering has finished successfully. The successfully
	// rendered chunks are not being rerendered due to the check at
	// the beginning of the function.
	args = append(args, "-y");

	args = append(args, "-ss", millisToSecsForFFmpeg(chunk.Start))
	for _, inFlag := range context.ExtraInFlags {
		args = append(args, inFlag)
	}
	args = append(args, "-i", chunk.InputPath)

	if len(context.VideoCodec) > 0 {
		args = append(args, "-c:v", context.VideoCodec)
	}
	if len(context.VideoBitrate) > 0 {
		args = append(args, "-vb", context.VideoBitrate)
	}
	if len(context.AudioCodec) > 0 {
		args = append(args, "-c:a", context.AudioCodec)
	}
	if len(context.AudioBitrate) > 0 {
		args = append(args, "-ab", context.AudioBitrate)
	}
	args = append(args, "-t", millisToSecsForFFmpeg(chunk.Duration()))
	if chunk.Blur {
		args = append(args, "-vf", "boxblur=50:5")
	}
	for _, outFlag := range context.ExtraOutFlags {
		args = append(args, outFlag)
	}
	unfinishedChunkName := "unfinished-chunk.mp4"
	args = append(args, unfinishedChunkName)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return err
	}

	fmt.Printf("INFO: Rename %s -> %s\n", unfinishedChunkName, chunk.Name());
	return os.Rename(unfinishedChunkName, chunk.Name())
}

func ffmpegConcatChunks(listPath string, outputPath string) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	// Unlike ffmpegCutChunk(), concatinating chunks is really
	// cheap. So we can just allow ourselves to always do that no
	// matter what.
	args = append(args, "-y")

	args = append(args, "-f", "concat")
	args = append(args, "-safe", "0")
	args = append(args, "-i", listPath)
	args = append(args, "-c", "copy")
	args = append(args, outputPath)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegFixupInput(inputPath, outputPath string, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	// ffmpeg -y -i {{ morning_input }} -codec copy -bsf:v h264_mp4toannexb {{ morning_input }}.fixed.ts
	args = append(args, "-i", inputPath)
	args = append(args, "-codec", "copy")
	args = append(args, "-bsf:v", "h264_mp4toannexb")
	args = append(args, outputPath)
	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegGenerateConcatList(chunks []Chunk, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, chunk := range chunks {
		fmt.Fprintf(f, "file '%s'\n", chunk.Name())
	}

	return nil
}

func captionsRingPush(ring []ChatMessage, message ChatMessage, capacity int) []ChatMessage {
	if len(ring) < capacity {
		return append(ring, message)
	}
	return append(ring[1:], message)
}

type Subcommand struct {
	Run         func(name string, args []string) bool
	Description string
}

var Subcommands = map[string]Subcommand{
	"fixup": {
		Description: "Fixup the initial footage",
		Run: func(name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ExitOnError)
			inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
			outputPtr := subFlag.String("output", "input.ts", "Path to the output video file")
			yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

			err := subFlag.Parse(args)
			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			if *inputPtr == "" {
				subFlag.Usage()
				fmt.Printf("ERROR: No -input file is provided\n")
				return false
			}

			err = ffmpegFixupInput(*inputPtr, *outputPtr, *yPtr)
			if err != nil {
				fmt.Printf("ERROR: Could not fixup input file %s: %s\n", *inputPtr, err)
				return false
			}
			fmt.Printf("Generated %s\n", *outputPtr)

			return true
		},
	},
	"cut": 	{
		Description: "Render specific cut of the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")

			err := subFlag.Parse(args)
			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext()
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			if len(context.cuts) == 0 {
				fmt.Printf("ERROR: No cuts are provided. Use `cut` command after a `chunk` command to define a cut\n");
				return false;
			}

			for _, cut := range context.cuts {
				if cut.chunk+1 >= len(context.chunks) {
					fmt.Printf("ERROR: %d is an invalid cut number. There is only %d of them.\n", cut.chunk, len(context.chunks)-1)
					return false
				}

				cutChunks := []Chunk{
					{
						Start: context.chunks[cut.chunk].End - cut.pad,
						End:   context.chunks[cut.chunk].End,
						InputPath: context.chunks[cut.chunk].InputPath,
					},
					{
						Start: context.chunks[cut.chunk+1].Start,
						End:   context.chunks[cut.chunk+1].Start + cut.pad,
						InputPath: context.chunks[cut.chunk+1].InputPath,
					},
				}

				for _, chunk := range cutChunks {
					err := ffmpegCutChunk(context, chunk)
					if err != nil {
						fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name(), err)
					}
				}

				cutListPath := "cut-%02d-list.txt"
				listPath := fmt.Sprintf(cutListPath, cut.chunk)
				err = ffmpegGenerateConcatList(cutChunks, listPath)
				if err != nil {
					fmt.Printf("ERROR: Could not generate not generate cut concat list %s: %s\n", cutListPath, err)
					return false
				}

				cutOutputPath := fmt.Sprintf("cut-%02d.mp4", cut.chunk)
				err = ffmpegConcatChunks(listPath, cutOutputPath)
				if err != nil {
					fmt.Printf("ERROR: Could not generate cut output file %s: %s\n", cutOutputPath, err)
					return false
				}

				fmt.Printf("Generated %s\n", cutOutputPath);
				fmt.Printf("%s: NOTE: cut is defined in here\n", context.chunks[cut.chunk].Loc);
			}

			return true
		},
	},
	"chunk": 	{
		Description: "Render specific chunk of the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")
			chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext();
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			if *chunkPtr > len(context.chunks) {
				fmt.Printf("ERROR: %d is an incorrect chunk number. There is only %d of them.\n", *chunkPtr, len(context.chunks))
				return false
			}

			chunk := context.chunks[*chunkPtr]

			err = ffmpegCutChunk(context, chunk)
			if err != nil {
				fmt.Printf("ERROR: Could not cut the chunk %s: %s\n", chunk.Name(), err)
				return false
			}

			fmt.Printf("%s is rendered!\n", chunk.Name())
			return true
		},
	},
	"final": 	{
		Description: "Render the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")

			err := subFlag.Parse(args)
			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext()
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			for _, chunk := range context.chunks {
				err := ffmpegCutChunk(context, chunk)
				if err != nil {
					fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name(), err)
				}
			}

			listPath := "final-list.txt"
			err = ffmpegGenerateConcatList(context.chunks, listPath)
			if err != nil {
				fmt.Printf("ERROR: Could not generate final concat list %s: %s\n", listPath, err)
				return false;
			}

			err = ffmpegConcatChunks(listPath, context.outputPath)
			if err != nil {
				fmt.Printf("ERROR: Could not generated final output %s: %s\n", context.outputPath, err)
				return false
			}

			context.PrintSummary()

			return true
		},
	},
	"summary": 	{
		Description: "Print the summary of the video",
		Run: func (name string, args []string) bool {
			summFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := summFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")

			err := summFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext();
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			context.PrintSummary()

			return true
		},
	},
	"chat": 	{
		Description: "Generate chat captions",
		Run: func (name string, args []string) bool {
			chatFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := chatFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")

			err := chatFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext()
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			capacity := 1
			ring := []ChatMessage{}
			timeCursor := Millis(0)
			subRipCounter := 0;
			for _, chunk := range context.chunks {
				prevTime := chunk.Start
				for _, message := range chunk.ChatLog {
					deltaTime := message.TimeOffset - prevTime
					prevTime = message.TimeOffset
					if len(ring) > 0 {
						subRipCounter += 1
						fmt.Printf("%d\n", subRipCounter);
						fmt.Printf("%s --> %s\n", millisToSubRipTs(timeCursor), millisToSubRipTs(timeCursor + deltaTime));
						for _, ringMessage := range ring {
							fmt.Printf("%s\n", ringMessage.Text);
						}
						fmt.Printf("\n")
					}
					timeCursor += deltaTime
					ring = captionsRingPush(ring, message, capacity);
				}
				timeCursor += chunk.End - prevTime
			}

			return true
		},
	},
	"prune": 	{
		Description: "Prune unused chunks",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context := defaultContext();
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			files, err := ioutil.ReadDir(ChunksFolder)
			if err != nil {
				fmt.Printf("ERROR: could not read %s folder: %s\n", ChunksFolder, err);
				return false;
			}

			for _, file := range files {
				if !file.IsDir() {
					filePath := fmt.Sprintf("%s/%s", ChunksFolder, file.Name());
					if !context.containsChunkWithName(filePath) {
						fmt.Printf("INFO: deleting chunk file %s\n", filePath);
						err = os.Remove(filePath)
						if err != nil {
							fmt.Printf("ERROR: could not remove file %s: %s\n", filePath, err)
							return false;
						}
					}
				}
			}

			fmt.Printf("DONE\n");

			return true
		},
	},
	// TODO: Maybe watch mode should just be a flag for the `final` subcommand
	"watch": 	{
		Description: "Render finished chunks in watch mode every time MARKUT file is modified",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the Markut file with markers (mandatory)")
			skipcatPtr := subFlag.Bool("skipcat", false, "Skip concatenation step")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			fmt.Printf("INFO: Waiting for updates to %s\n", *markutPtr)
			for {
				// NOTE: always use rsync(1) for updating the MARKUT file remotely.
				// This kind of crappy modification checking needs at least some sort of atomicity.
				// rsync(1) is as atomic as rename(2). So it's alright for majority of the cases.

				context := defaultContext();
				ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
				if !ok {
					return false
				}

				done := true
				for _, chunk := range(context.chunks) {
					if chunk.Unfinished {
						done = false
						continue
					}

					if _, err := os.Stat(chunk.Name()); errors.Is(err, os.ErrNotExist) {
						err = ffmpegCutChunk(context, chunk)
						if err != nil {
							fmt.Printf("ERROR: Could not cut the chunk %s: %s\n", chunk.Name(), err)
							return false
						}
						fmt.Printf("INFO: Waiting for more updates to %s\n", *markutPtr)
						done = false
						break
					}
				}

				if done {
					break
				}

				time.Sleep(1 * time.Second)
			}

			context := defaultContext()
			ok := context.evalMarkutFile(*markutPtr) && context.finishEval()
			if !ok {
				return false
			}

			if !*skipcatPtr {

				listPath := "final-list.txt"
				err = ffmpegGenerateConcatList(context.chunks, listPath)
				if err != nil {
					fmt.Printf("ERROR: Could not generate final concat list %s: %s\n", listPath, err)
					return false;
				}

				err = ffmpegConcatChunks(listPath, context.outputPath)
				if err != nil {
					fmt.Printf("ERROR: Could not generated final output %s: %s\n", context.outputPath, err)
					return false
				}
			}

			context.PrintSummary()

			return true
		},
	},
	"funcs": {
		Description: "Print info about all the available funcs of the Markut Language",
		Run: func (commandName string, args []string) bool {
			if len(args) > 0 {
				name := args[0]
				funk, ok := funcs[name]
				if !ok {
					fmt.Printf("ERROR: no func named %s is found\n", name);
					return false;
				}
				fmt.Printf("%s : %s\n", name, funk.Signature);
				fmt.Printf("    %s\n", strings.ReplaceAll(funk.Description, "$SPOILER$", ""));
				return true;
			}

			names := []string{};
			for name, _ := range funcs {
				names = append(names, name)
			}
			sort.Slice(names, func(i, j int) bool {
				return names[i] < names[j]
			})
			sort.SliceStable(names, func(i, j int) bool { // Rare moment in my boring dev life when I actually need a stable sort
				return funcs[names[i]].Category < funcs[names[j]].Category
			})
			if len(names) > 0 {
				category := ""
				for _, name := range(names) {
					if category != funcs[name].Category {
						category = funcs[name].Category
						fmt.Printf("%s:\n", category)
					}
					fmt.Printf("    %s - %s\n", name, strings.Split(funcs[name].Description, "$SPOILER$")[0]);
				}
			}
			return true;
		},
	},
}

func usage() {
	names := []string{};
	for name, _ := range Subcommands {
		names = append(names, name)
	}
	sort.Strings(names);
	fmt.Printf("Usage: markut <SUBCOMMAND> [OPTIONS]\n")
	fmt.Printf("SUBCOMMANDS:\n")
	for _, name := range names {
		fmt.Printf("    %s - %s\n", name, Subcommands[name].Description)
	}
	fmt.Printf("ENVARS:\n")
	fmt.Printf("    FFMPEG_PREFIX      Prefix path for a custom ffmpeg distribution\n")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		fmt.Printf("ERROR: No subcommand is provided\n")
		os.Exit(1)
	}

	name := os.Args[1];
	args := os.Args[2:];
	subcommand, ok := Subcommands[name];
	if !ok {
		usage()
		fmt.Printf("ERROR: Unknown subcommand %s\n", name)
		os.Exit(1)
	}
	if !subcommand.Run(name, args) {
		os.Exit(1)
	}
}
