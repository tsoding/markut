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
	"encoding/csv"
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
	Nickname string
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

func typeCheckArgs(loc Loc, argsStack []Token, signature ...TokenKind) (args []Token, err error, nextStack []Token) {
	if len(argsStack) < len(signature) {
		err = &DiagErr{
			Loc: loc,
			Err: fmt.Errorf("Expected %d arguments but got %d", len(signature), len(argsStack)),
		}
		return
	}

	for _, kind := range signature {
		n := len(argsStack)
		arg := argsStack[n-1]
		argsStack = argsStack[:n-1]
		if kind != arg.Kind {
			err = &DiagErr{
				Loc: arg.Loc,
				Err: fmt.Errorf("Expected %s but got %s", TokenKindName[kind], TokenKindName[arg.Kind]),
			}
			return
		}
		args = append(args, arg)
	}

	nextStack = argsStack

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
	modified_cuts []int

	VideoCodec string
	VideoBitrate string
	AudioCodec string
	AudioBitrate string

	ExtraOutFlags []string
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

// This function is compatible with the format https://www.twitchchatdownloader.com/ generates
func loadTwitchChatDownloaderCSV(path string) ([]ChatMessage, error) {
	chatLog := []ChatMessage{}
	f, err := os.Open(path);
	if err != nil {
		return chatLog, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = ','
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return chatLog, err
	}
	for i := range records {
		secs, err := strconv.Atoi(records[i][0])
		if err != nil {
			return chatLog, fmt.Errorf("%s:%d: invalid timestamp: %w", path, i, err)
		}
		chatLog = append(chatLog, ChatMessage{
			TimeOffset: Millis(secs*1000),
			Nickname: records[i][1],
			Text: records[i][3],
		})
	}
	sort.Slice(chatLog, func(i, j int) bool {
		return chatLog[i].TimeOffset < chatLog[i].TimeOffset
	})
	return chatLog, nil
}

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
			Nickname: nickname,
			Text: text,
		})
	}

	sort.Slice(chatLog, func(i, j int) bool {
		return chatLog[i].TimeOffset < chatLog[i].TimeOffset
	})

	return chatLog, nil
}

func evalMarkutFile(path string) (context EvalContext, ok bool) {
	// Default chunk transcoding parameters
	context.VideoCodec = "libx264"
	context.VideoBitrate = "4000k"
	context.AudioCodec = "aac"
	context.AudioBitrate = "300k"
	context.outputPath = "output.mp4"

	ok = true
	content, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("ERROR: could not read file %s: %s\n", path, err)
		ok = false
		return
	}

	lexer := NewLexer(string(content), path)
	token := Token{}
	argsStack := []Token{}
	chapStack := []Chapter{}
	var chapOffset Millis = 0
	for {
		token, err = lexer.Next()
		if err != nil {
			fmt.Printf("%s\n", err)
			ok = false
			return
		}

		if token.Kind == TokenEOF {
			break
		}

		var args []Token
		switch token.Kind {
		case TokenDash:
			args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for subtraction\n", token.Loc)
				fmt.Printf("%s\n", err);
				ok = false
				return
			}
			argsStack = append(argsStack, Token{
				Loc: token.Loc,
				Kind: TokenTimestamp,
				Timestamp: args[1].Timestamp - args[0].Timestamp,
			})
		case TokenPlus:
			args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp, TokenTimestamp)
			if err != nil {
				fmt.Printf("%s: ERROR: type check failed for addition\n", token.Loc)
				fmt.Printf("%s\n", err);
				ok = false
				return
			}
			argsStack = append(argsStack, Token{
				Loc: token.Loc,
				Kind: TokenTimestamp,
				Timestamp: args[1].Timestamp + args[0].Timestamp,
			})
		case TokenString:
			fallthrough
		case TokenTimestamp:
			argsStack = append(argsStack, token)
		case TokenSymbol:
			command := string(token.Text)
			switch command {
			case "video_codec":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				context.VideoCodec = string(args[0].Text)
			case "video_bitrate":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				context.VideoBitrate = string(args[0].Text)
			case "audio_codec":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				context.AudioCodec = string(args[0].Text)
			case "audio_bitrate":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				context.AudioBitrate = string(args[0].Text)
			case "of":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				outFlag := args[0]
				context.ExtraOutFlags = append(context.ExtraOutFlags, string(outFlag.Text))
			case "chat":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				path := args[0]
				context.chatLog, err = loadTwitchChatDownloaderCSV(string(path.Text))
				if err != nil {
					fmt.Printf("%s: ERROR: could not load the chat logs: %s\n", path.Loc, err)
					ok = false
					return
				}
			case "no_chat":
				context.chatLog = []ChatMessage{}
			case "input":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				path := args[0]
				if len(path.Text) == 0 {
					fmt.Printf("%s: ERROR: cannot set empty input path\n", path.Loc);
					ok = false
					return
				}
				context.inputPath = string(path.Text)
			case "over":
				arity := 2
				if len(argsStack) < arity {
					err = &DiagErr{
						Loc: token.Loc,
						Err: fmt.Errorf("Expected %d arguments but got %d", arity, len(argsStack)),
					}
				}
				n := len(argsStack)
				argsStack = append(argsStack, argsStack[n-2]);
			case "dup":
				arity := 1
				if len(argsStack) < arity {
					err = &DiagErr{
						Loc: token.Loc,
						Err: fmt.Errorf("Expected %d arguments but got %d", arity, len(argsStack)),
					}
					return
				}
				n := len(argsStack)
				// TODO: the location of the dupped value should be the location of the "dup" token
				argsStack = append(argsStack, argsStack[n-1])
			case "chapter":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				chapStack = append(chapStack, Chapter{
					Loc: args[1].Loc,
					Label: string(args[0].Text),
					Timestamp: args[1].Timestamp,
				})
			case "puts":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				fmt.Printf("%s", string(args[0].Text));
			case "putd":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				fmt.Printf("%d", int(args[0].Timestamp));
			case "putt":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				fmt.Printf("%s", millisToTs(args[0].Timestamp));
			case "here":
				argsStack = append(argsStack, Token{
					Loc: token.Loc,
					Kind: TokenString,
					Text: []rune(token.Loc.String()),
				})
			case "chunk_location":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					ok = false
					return
				}

				argsStack = append(argsStack, Token{
					Loc: token.Loc,
					Kind: TokenString,
					Text: []rune(context.chunks[n-1].Loc.String()),
				})
			case "chunk_number":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					ok = false
					return
				}

				argsStack = append(argsStack, Token{
					Loc: token.Loc,
					Kind: TokenTimestamp,
					Timestamp: Millis(n-1),
				})
			case "chunk_duration":
				n := len(context.chunks)
				if n == 0 {
					fmt.Printf("%s: ERROR: no chunks defined\n", token.Loc)
					ok = false
					return
				}

				argsStack = append(argsStack, Token{
					Loc: token.Loc,
					Kind: TokenTimestamp,
					Timestamp: context.chunks[n-1].Duration(),
				})
			case "modified_cut":
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for a modified_cut\n", token.Loc)
					ok = false
					return
				}
				context.modified_cuts = append(context.modified_cuts, len(context.chunks) - 1)
			case "blur":
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for a blur\n", token.Loc)
					ok = false
					return
				}
				context.chunks[len(context.chunks)-1].Blur = true
			case "removed":
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for removal\n", token.Loc)
					ok = false
					return
				}
				context.chunks = context.chunks[:len(context.chunks)-1]
			case "unfinished":
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for marking as unfinished\n", token.Loc)
					ok = false
					return
				}
				context.chunks[len(context.chunks)-1].Unfinished = true
			case "cut":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}
				pad := args[0]
				if len(context.chunks) == 0 {
					fmt.Printf("%s: ERROR: no chunks defined for a cut\n", token.Loc)
					ok = false
					return
				}
				context.cuts = append(context.cuts, Cut{
					chunk: len(context.chunks) - 1,
					pad: pad.Timestamp,
				})
			case "chunk":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					ok = false
					return
				}

				start := args[1]
				end := args[0]

				if start.Timestamp < 0 {
					fmt.Printf("%s: ERROR: the start of the chunk is negative %s\n", start.Loc, millisToTs(start.Timestamp));
					ok = false
					return
				}

				if end.Timestamp < 0 {
					fmt.Printf("%s: ERROR: the end of the chunk is negative %s\n", end.Loc, millisToTs(end.Timestamp));
					ok = false
					return
				}

				if start.Timestamp > end.Timestamp {
					fmt.Printf("%s: ERROR: the end of the chunk %s is earlier than its start %s\n", end.Loc, millisToTs(end.Timestamp), millisToTs(start.Timestamp));
					fmt.Printf("%s: NOTE: the start is located here\n", start.Loc);
					ok = false
					return
				}

				chunk := Chunk{
					Loc: token.Loc,
					Start: start.Timestamp,
					End: end.Timestamp,
					InputPath: context.inputPath,
					ChatLog: sliceChatLog(context.chatLog, start.Timestamp, end.Timestamp),
				}

				context.chunks = append(context.chunks, chunk)

				for _, chapter := range chapStack {
					if chapter.Timestamp < chunk.Start || chunk.End < chapter.Timestamp {
						fmt.Printf("%s: ERROR: the timestamp %s of chapter \"%s\" is outside of the the current chunk\n", chapter.Loc, millisToTs(chapter.Timestamp), chapter.Label)
						fmt.Printf("%s: NOTE: which starts at %s\n", start.Loc, millisToTs(start.Timestamp))
						fmt.Printf("%s: NOTE: and ends at %s\n", end.Loc, millisToTs(end.Timestamp))
						ok = false
						return
					}

					context.chapters = append(context.chapters, Chapter{
						Loc: chapter.Loc,
						Timestamp: chapter.Timestamp - chunk.Start + chapOffset,
						Label: chapter.Label,
					})
				}

				chapOffset += chunk.End - chunk.Start

				chapStack = []Chapter{}
			default:
				fmt.Printf("%s: ERROR: Unknown command %s\n", token.Loc, command)
				ok = false
				return
			}
		default:
			fmt.Printf("%s: ERROR: Unexpected token %s\n", token.Loc, TokenKindName[token.Kind]);
			ok = false;
			return
		}
	}

	for i := 0; i + 1 < len(context.chapters); i += 1 {
		duration := context.chapters[i + 1].Timestamp - context.chapters[i].Timestamp;
		// TODO: angled brackets are not allowed on YouTube. Let's make `chapters` check for that too.
		if duration < MinYouTubeChapterDuration {
			fmt.Printf("%s: ERROR: the chapter \"%s\" has duration %s which is shorter than the minimal allowed YouTube chapter duration which is %s (See https://support.google.com/youtube/answer/9884579)\n", context.chapters[i].Loc, context.chapters[i].Label, millisToTs(duration), millisToTs(MinYouTubeChapterDuration));
			fmt.Printf("%s: NOTE: the chapter ends here\n", context.chapters[i + 1].Loc);
			ok = false;
			return;
		}
	}

	if len(argsStack) > 0 {
		ok = false;
		for i := range argsStack {
			fmt.Printf("%s: ERROR: unused argument\n", argsStack[i].Loc)
		}
	}

	if len(chapStack) > 0 {
		ok = false;
		for i := range chapStack {
			fmt.Printf("%s: ERROR: unused chapter\n", chapStack[i].Loc)
		}
	}

	return
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
	// TODO: Maybe we should remove this check? We already pass -n to
	// ffmpeg down there...
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

	// Rendering chunks is expensive. So if the chunk already exists,
	// we do not rerender it.
	args = append(args, "-n");

	args = append(args, "-ss", millisToSecsForFFmpeg(chunk.Start))
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

// TODO: `final` subcommand should not render already cached chunks. Just like the watch mode.
func finalSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("final", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	patchPtr := subFlag.Bool("patch", false, "Patch modified cuts")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		subFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n")
		return false
	}


	context, ok := evalMarkutFile(*markutPtr)
	if !ok {
		return false
	}

	if *patchPtr {
		for _, i := range context.modified_cuts {
			chunk := context.chunks[i]
			err := ffmpegCutChunk(context, chunk)
			if err != nil {
				fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk, err)
			}
			if i+1 < len(context.chunks) {
				chunk = context.chunks[i+1]
				err = ffmpegCutChunk(context, chunk)
				if err != nil {
					fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name(), err)
				}
			}
		}
	} else {
		for _, chunk := range context.chunks {
			err := ffmpegCutChunk(context, chunk)
			if err != nil {
				fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name(), err)
			}
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
}

func cutSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("cut", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		subFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n")
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
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
}

func summarySubcommand(args []string) bool {
	summFlag := flag.NewFlagSet("summary", flag.ContinueOnError)
	markutPtr := summFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := summFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		summFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n");
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
	if !ok {
		return false
	}

	context.PrintSummary()

	return true
}

func captionsRingPush(ring []ChatMessage, message ChatMessage, capacity int) []ChatMessage {
	if len(ring) < capacity {
		return append(ring, message)
	}
	return append(ring[1:], message)
}

func chatSubcommand(args []string) bool {
	chatFlag := flag.NewFlagSet("chat", flag.ContinueOnError)
	markutPtr := chatFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := chatFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		chatFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n");
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
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
					fmt.Printf("[%s] %s\n", ringMessage.Nickname, ringMessage.Text)
				}
				fmt.Printf("\n")
			}
			timeCursor += deltaTime
			ring = captionsRingPush(ring, message, capacity);
		}
		timeCursor += chunk.End - prevTime
	}

	return true
}

func chunkSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("chunk", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")

	err := subFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		subFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n")
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
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
}

func fixupSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("fixup", flag.ExitOnError)
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
}

func pruneSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("prune", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := subFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		subFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n")
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
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
}

// TODO: Maybe watch mode should just a flag for the `final` subcommand
func watchSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("watch", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	skipcatPtr := subFlag.Bool("skipcat", false, "Skip concatenation step")

	err := subFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		subFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n")
		return false
	}

	fmt.Printf("INFO: Waiting for updates to %s\n", *markutPtr)
	for {
		// NOTE: always use rsync(1) for updating the MARKUT file remotely.
		// This kind of crappy modification checking needs at least some sort of atomicity.
		// rsync(1) is as atomic as rename(2). So it's alright for majority of the cases.

		context, ok := evalMarkutFile(*markutPtr)
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

	context, ok := evalMarkutFile(*markutPtr)
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
}

type Subcommand struct {
	Name        string
	Run         func(args []string) bool
	Description string
}

var Subcommands = []Subcommand{
	{
		Name:        "fixup",
		Run:         fixupSubcommand,
		Description: "Fixup the initial footage",
	},
	{
		Name:        "cut",
		Run:         cutSubcommand,
		Description: "Render specific cut of the final video",
	},
	{
		Name:        "chunk",
		Run:         chunkSubcommand,
		Description: "Render specific chunk of the final video",
	},
	{
		Name:        "final",
		Run:         finalSubcommand,
		Description: "Render the final video",
	},
	{
		Name: "summary",
		Run: summarySubcommand,
		Description: "Print the summary of the video",
	},
	{
		Name: "chat",
		Run: chatSubcommand,
		Description: "Generation chat captions",
	},
	{
		Name: "prune",
		Run: pruneSubcommand,
		Description: "Prune unused chunks",
	},
	{
		Name: "watch",
		Run: watchSubcommand,
		Description: "Render finished chunks in watch mode every time MARKUT file is modified",
	},
}

func usage() {
	fmt.Printf("Usage: markut <SUBCOMMAND> [OPTIONS]\n")
	fmt.Printf("SUBCOMMANDS:\n")
	for _, subcommand := range Subcommands {
		fmt.Printf("    %s - %s\n", subcommand.Name, subcommand.Description)
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

	for _, subcommand := range Subcommands {
		if subcommand.Name == os.Args[1] {
			ok := subcommand.Run(os.Args[2:])
			if !ok {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	usage()
	fmt.Printf("ERROR: Unknown subcommand %s\n", os.Args[1])
	os.Exit(1)
}
