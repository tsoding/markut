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
	"regexp"
	"slices"
	"net/http"
	"encoding/json"
)

func decomposeMillis(millis Millis) (hh int64, mm int64, ss int64, ms int64, sign string) {
	sign = ""
	if millis < 0 {
		sign = "-"
		millis = -millis
	}
	hh = int64(millis / 1000 / 60 / 60)
	mm = int64(millis / 1000 / 60 % 60)
	ss = int64(millis / 1000 % 60)
	ms = int64(millis % 1000)
	return
}

// Timestamp format used by Markut Language
func millisToTs(millis Millis) string {
	hh, mm, ss, ms, sign := decomposeMillis(millis)
	return fmt.Sprintf("%s%02d:%02d:%02d.%03d", sign, hh, mm, ss, ms)
}

// Timestamp format used on YouTube
func millisToYouTubeTs(millis Millis) string {
	hh, mm, ss, _, sign := decomposeMillis(millis)
	return fmt.Sprintf("%s%02d:%02d:%02d", sign, hh, mm, ss)
}

// Timestamp format used by SubRip https://en.wikipedia.org/wiki/SubRip that we
// use for generating the chat in subtitles on YouTube
func millisToSubRipTs(millis Millis) string {
	hh, mm, ss, ms, sign := decomposeMillis(millis)
	return fmt.Sprintf("%s%02d:%02d:%02d,%03d", sign, hh, mm, ss, ms)
}

type ChatMessage struct {
	Nickname string
	Color string
	Text string
}

type ChatMessageGroup struct {
	TimeOffset Millis
	Messages []ChatMessage
}

type Chunk struct {
	Start Millis
	End Millis
	Loc Loc
	InputPath string
	ChatLog []ChatMessageGroup
	Blur bool
	Unfinished bool
}

const ChunksFolder = "chunks"
const TwitchChatDownloaderCSVHeader = "time,user_name,user_color,message"

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

func (chunk Chunk) Rendered() (bool, error) {
	_, err := os.Stat(chunk.Name())
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
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
	inputPathLog []Token
	outputPath string
	chatLog []ChatMessageGroup
	chunks []Chunk
	chapters []Chapter
	cuts []Cut

	argsStack []Token
	chapStack []Chapter
	chapOffset Millis

	VideoCodec *Token
	VideoBitrate *Token
	AudioCodec *Token
	AudioBitrate *Token

	ExtraOutFlags []Token
	ExtraInFlags []Token
}

const (
	DefaultVideoCodec = "libx264"
	DefaultVideoBitrate = "4000k"
	DefaultAudioCodec = "aac"
	DefaultAudioBitrate = "300k"
)

func defaultContext() (EvalContext, bool) {
	context := EvalContext{
		outputPath: "output.mp4",
	}

	if home, ok := os.LookupEnv("HOME"); ok {
		path := path.Join(home, ".markut");
		content, err := ioutil.ReadFile(path);
		if err != nil {
			if os.IsNotExist(err) {
				return context, true;
			}
			fmt.Printf("ERROR: Could not open %s to read as a config: %s\n", path, err);
			return context, false;
		}
		if !context.evalMarkutContent(string(content), path) {
			return context, false
		}
	}
	return context, true;
}

func (context EvalContext) PrintSummary() error {
	fmt.Printf(">>> Main Output Parameters:\n")
	if context.VideoCodec != nil {
		fmt.Printf("Video Codec:   %s (Defined at %s)\n", string(context.VideoCodec.Text), context.VideoCodec.Loc);
	} else {
		fmt.Printf("Video Codec:   %s (Default)\n", DefaultVideoCodec);
	}
	if context.VideoBitrate != nil {
		fmt.Printf("Video Bitrate: %s (Defined at %s)\n", string(context.VideoBitrate.Text), context.VideoBitrate.Loc);
	} else {
		fmt.Printf("Video Bitrate: %s (Default)\n", DefaultVideoBitrate);
	}
	if context.AudioCodec != nil {
		fmt.Printf("Audio Codec:   %s (Defined at %s)\n", string(context.AudioCodec.Text), context.AudioCodec.Loc);
	} else {
		fmt.Printf("Audio Codec:   %s (Default)\n", DefaultAudioCodec);
	}
	if context.AudioBitrate != nil {
		fmt.Printf("Audio Bitrate: %s (Defined at %s)\n", string(context.AudioBitrate.Text), context.AudioBitrate.Loc);
	} else {
		fmt.Printf("Audio Bitrate: %s (Default)\n", DefaultAudioBitrate);
	}
	fmt.Println()
	// TODO: merge together parameters defined on the same line
	if len(context.ExtraInFlags) > 0 {
		fmt.Printf(">>> Extra Input Parameters:\n")
		for _, inFlag := range context.ExtraInFlags {
			fmt.Printf("%s: %s\n", inFlag.Loc, string(inFlag.Text));
		}
		fmt.Println()
	}
	if len(context.ExtraOutFlags) > 0 {
		fmt.Printf(">>> Extra Output Parameters:\n")
		for _, outFlag := range context.ExtraOutFlags {
			fmt.Printf("%s: %s\n", outFlag.Loc, string(outFlag.Text));
		}
		fmt.Println()
	}
	TwitchVodFileRegexp := "([0-9]+)-[0-9a-f\\-]+\\.mp4"
	re := regexp.MustCompile(TwitchVodFileRegexp)
	fmt.Printf(">>> Twitch Chat Logs (Detected by regex `%s`)\n", TwitchVodFileRegexp)
	for _, inputPath := range context.inputPathLog {
		match := re.FindStringSubmatch(string(inputPath.Text))
		if len(match) > 0 {
			fmt.Printf("%s: https://www.twitchchatdownloader.com/video/%s\n", inputPath.Loc, match[1])
		} else {
			fmt.Printf("%s: NO MATCH\n", inputPath.Loc)
		}
	}
	fmt.Println()
	fmt.Printf(">>> Cuts (%d):\n", max(len(context.chunks) - 1, 0))
	var fullLength Millis = 0
	var finishedLength Millis = 0
	var renderedLength Millis = 0
	for i, chunk := range context.chunks {
		if i < len(context.chunks) - 1 {
			fmt.Printf("%s: Cut %d - %s\n", chunk.Loc, i, millisToTs(fullLength + chunk.Duration()))
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
	fmt.Printf(">>> Chunks (%d):\n", len(context.chunks))
	for index, chunk := range context.chunks {
		rendered, err := chunk.Rendered();
		if err != nil {
			return nil
		}
		checkMark := "[ ]"
		if rendered {
			checkMark = "[x]"
		}
		fmt.Printf("%s: %s Chunk %d - %s -> %s (Duration: %s)\n", chunk.Loc, checkMark, index, millisToTs(chunk.Start), millisToTs(chunk.End), millisToTs(chunk.Duration()))
	}
	fmt.Println()
	fmt.Printf(">>> YouTube Chapters (%d):\n", len(context.chapters))
	for _, chapter := range context.chapters {
		fmt.Printf("- %s - %s\n", millisToYouTubeTs(chapter.Timestamp), chapter.Label)
	}
	fmt.Println()
	fmt.Printf(">>> Length:\n")
	fmt.Printf("Rendered Length: %s\n", millisToTs(renderedLength))
	fmt.Printf("Finished Length: %s\n", millisToTs(finishedLength))
	fmt.Printf("Full Length:     %s\n", millisToTs(fullLength))
	return nil
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
func sliceChatLog(chatLog []ChatMessageGroup, start, end Millis) []ChatMessageGroup {
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
	return []ChatMessageGroup{}
}

// IMPORTANT! chatLog is assumed to be sorted by TimeOffset.
func compressChatLog(chatLog []ChatMessageGroup) []ChatMessageGroup {
	result := []ChatMessageGroup{}
	for i := range chatLog {
		if len(result) > 0 && result[len(result)-1].TimeOffset == chatLog[i].TimeOffset {
			result[len(result)-1].Messages = append(result[len(result)-1].Messages, chatLog[i].Messages...)
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

var funcs map[string]Func;

// This function is compatible with the format https://www.twitchchatdownloader.com/ generates.
// It does not use encoding/csv because that website somehow generates unparsable garbage.
func loadTwitchChatDownloaderCSVButParseManually(path string) ([]ChatMessageGroup, error) {
	chatLog := []ChatMessageGroup{}
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
		if i == 0 && line == TwitchChatDownloaderCSVHeader {
			// If first line contains the TwitchChatDownloader's stupid header, just ignore it. Just let people have it.
			continue
		}
		if len(line) == 0 {
			// We encounter empty line usually at the end of the file. So it should be safe to break.
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
		color := pair[0]
		text := pair[1]

		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			text = text[1:len(text)-1]
		}

		chatLog = append(chatLog, ChatMessageGroup{
			TimeOffset: Millis(secs*1000),
			Messages: []ChatMessage{
				{Color: color, Nickname: nickname, Text: text},
			},
		})
	}

	sort.Slice(chatLog, func(i, j int) bool {
		return chatLog[i].TimeOffset < chatLog[j].TimeOffset
	})

	return compressChatLog(chatLog), nil
}

func (context *EvalContext) evalMarkutContent(content string, path string) bool {
	lexer := NewLexer(content, path)
	token := Token{}
	var err error
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
			f, ok := funcs[command];
			if !ok {
				fmt.Printf("%s: ERROR: Unknown command %s\n", token.Loc, command)
				return false
			}
			if !f.Run(context, command, token) {
				return false
			}
		default:
			fmt.Printf("%s: ERROR: Unexpected token %s\n", token.Loc, TokenKindName[token.Kind]);
			return false
		}
	}

	return true
}

func (context *EvalContext) evalMarkutFile(loc *Loc, path string, ignoreIfMissing bool) bool {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		sb := strings.Builder{}
		if loc != nil {
			sb.WriteString(fmt.Sprintf("%s: ", *loc));
		}
		if ignoreIfMissing {
			sb.WriteString("WARNING: ")
		} else {
			sb.WriteString("ERROR: ")
		}
		sb.WriteString(fmt.Sprintf("%s", err))
		fmt.Fprintf(os.Stderr, "%s\n", sb.String())
		return ignoreIfMissing
	}

	return context.evalMarkutContent(string(content), path)
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
	// TODO: replace FFMPEG_PREFIX envar in favor of a func `ffmpeg_prefix` that you have to call in $HOME/.markut
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
	rendered, err := chunk.Rendered();
	if err != nil {
		return err;
	}

	if rendered {
		fmt.Printf("INFO: %s is already rendered\n", chunk.Name());
		return nil;
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
		args = append(args, string(inFlag.Text))
	}
	args = append(args, "-i", chunk.InputPath)

	if context.VideoCodec != nil {
		args = append(args, "-c:v", string(context.VideoCodec.Text))
	} else {
		args = append(args, "-c:v", DefaultVideoCodec)
	}
	if context.VideoBitrate != nil {
		args = append(args, "-vb", string(context.VideoBitrate.Text))
	} else {
		args = append(args, "-vb", DefaultVideoBitrate)
	}
	if context.AudioCodec != nil {
		args = append(args, "-c:a", string(context.AudioCodec.Text))
	} else {
		args = append(args, "-c:a", DefaultAudioCodec)
	}
	if context.AudioBitrate != nil {
		args = append(args, "-ab", string(context.AudioBitrate.Text))
	} else {
		args = append(args, "-ab", DefaultAudioBitrate)
	}
	args = append(args, "-t", millisToSecsForFFmpeg(chunk.Duration()))
	if chunk.Blur {
		args = append(args, "-vf", "boxblur=50:5")
	}
	for _, outFlag := range context.ExtraOutFlags {
		args = append(args, string(outFlag.Text))
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

func captionsRingPush(ring []ChatMessageGroup, message ChatMessageGroup, capacity int) []ChatMessageGroup {
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
	"cut": {
		Description: "Render specific cut of the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the MARKUT file")

			err := subFlag.Parse(args)
			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext()
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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
	"chunk": {
		Description: "Render specific chunk of the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the MARKUT file")
			chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext();
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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
	"final": {
		Description: "Render the final video",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the MARKUT file")

			err := subFlag.Parse(args)
			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext()
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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

			err = context.PrintSummary()
			if err != nil {
				fmt.Printf("ERROR: Could not print summary: %s\n", err);
				return false
			}

			return true
		},
	},
	"summary": {
		Description: "Print the summary of the video",
		Run: func (name string, args []string) bool {
			summFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := summFlag.String("markut", "MARKUT", "Path to the MARKUT file")

			err := summFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext();
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
			if !ok {
				return false
			}

			err = context.PrintSummary()
			if err != nil {
				fmt.Printf("ERROR: Could not print summary: %s\n", err)
				return false
			}

			return true
		},
	},
	"chat": {
		Description: "Generate chat captions",
		Run: func (name string, args []string) bool {
			chatFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := chatFlag.String("markut", "MARKUT", "Path to the MARKUT file")
			csvPtr := chatFlag.Bool("csv", false, "Generate the chat using the stupid Twich Chat Downloader CSV format. You can then feed this output to tools like SubChat https://github.com/Kam1k4dze/SubChat")

			err := chatFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext()
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
			if !ok {
				return false
			}

			if *csvPtr {
				fmt.Printf("%s\n", TwitchChatDownloaderCSVHeader);
				var cursor Millis = 0
				for _, chunk := range context.chunks {
					for _, messageGroup := range chunk.ChatLog {
						timestamp := cursor + messageGroup.TimeOffset - chunk.Start;
						for _, message := range messageGroup.Messages {
							fmt.Printf("%d,%s,%s,\"%s\"\n", timestamp, message.Nickname, message.Color, message.Text);
						}
					}
					cursor += chunk.End - chunk.Start;
				}
			} else {
				capacity := 1
				ring := []ChatMessageGroup{}
				timeCursor := Millis(0)
				subRipCounter := 0;
				sb := strings.Builder{}
				for _, chunk := range context.chunks {
					prevTime := chunk.Start
					for _, message := range chunk.ChatLog {
						deltaTime := message.TimeOffset - prevTime
						prevTime = message.TimeOffset
						if len(ring) > 0 {
							subRipCounter += 1
							fmt.Printf("%d\n", subRipCounter);
							fmt.Printf("%s --> %s\n", millisToSubRipTs(timeCursor), millisToSubRipTs(timeCursor + deltaTime));
							for _, ringMessageGroup := range ring {
								sb.Reset();
								for _, message := range ringMessageGroup.Messages {
									sb.WriteString(fmt.Sprintf("[%s] %s\n", message.Nickname, message.Text));
								}
								fmt.Printf("%s", sb.String());
							}
							fmt.Printf("\n")
						}
						timeCursor += deltaTime
						ring = captionsRingPush(ring, message, capacity);
					}
					timeCursor += chunk.End - prevTime
				}
			}

			return true
		},
	},
	"prune": {
		Description: "Prune unused chunks",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the MARKUT file")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			context, ok := defaultContext();
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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
	"watch": {
		Description: "Render finished chunks in watch mode every time MARKUT file is modified",
		Run: func (name string, args []string) bool {
			subFlag := flag.NewFlagSet(name, flag.ContinueOnError)
			markutPtr := subFlag.String("markut", "MARKUT", "Path to the MARKUT file")
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

				context, ok := defaultContext();
				ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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

			context, ok := defaultContext()
			ok = ok && context.evalMarkutFile(nil, *markutPtr, false) && context.finishEval()
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

			err = context.PrintSummary()
			if err != nil {
				fmt.Printf("ERROR: Could not print summary: %s\n", err);
				return false
			}

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
	"twitch-chat-download": {
		Description: "Download Twitch Chat of a VOD and print it in the stupid format https://twitchchatdownloader.com/ uses to maintain compatibility with our existing chat parser",
		Run: func (commandName string, args []string) bool {
			subFlag := flag.NewFlagSet(commandName, flag.ContinueOnError)
			videoIdPtr := subFlag.String("videoID", "", "Video ID of the Twitch VOD to download")

			err := subFlag.Parse(args)

			if err == flag.ErrHelp {
				return true
			}

			if err != nil {
				fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
				return false
			}

			if (*videoIdPtr == "") {
				subFlag.Usage()
				fmt.Printf("ERROR: No -videoID is provided\n")
				return false
			}

			client := &http.Client{}

			queryMessagesByOffset := func(videoId, gqlCursorId string) (string, bool) {
				// twitchClientId := "kimne78kx3ncx6brgo4mv6wki5h1ko" // This is the Client ID of the Twitch Web App itself
				twitchClientId := "kd1unb4b3q4t58fwlpcbzcbnm76a8fp" // https://github.com/ihabunek/twitch-dl/issues/124#issuecomment-1537030937
				gqlUrl := "https://gql.twitch.tv/gql"
				var body string
				if gqlCursorId == "" {
					body = fmt.Sprintf("[{\"operationName\":\"VideoCommentsByOffsetOrCursor\",\"variables\":{\"videoID\":\"%s\",\"contentOffsetSeconds\":0},\"extensions\":{\"persistedQuery\":{\"version\":1,\"sha256Hash\":\"b70a3591ff0f4e0313d126c6a1502d79a1c02baebb288227c582044aa76adf6a\"}}}]", videoId)
				} else {
					body = fmt.Sprintf("[{\"operationName\":\"VideoCommentsByOffsetOrCursor\",\"variables\":{\"videoID\":\"%s\",\"cursor\":\"%s\"},\"extensions\":{\"persistedQuery\":{\"version\":1,\"sha256Hash\":\"b70a3591ff0f4e0313d126c6a1502d79a1c02baebb288227c582044aa76adf6a\"}}}]", videoId, gqlCursorId)
				}
				req, err := http.NewRequest("POST", gqlUrl, strings.NewReader(body))
				if err != nil {
					fmt.Printf("ERROR: could not create request for url %s: %s\n", gqlUrl, err)
					return "", false
				}
				req.Header.Add("Client-Id", twitchClientId)
				resp, err := client.Do(req)
				if err != nil {
					fmt.Printf("ERROR: could not perform POST request to %s: %s\n", gqlUrl, err)
					return "", false
				}
				defer resp.Body.Close()

				var root interface{}
				decoder := json.NewDecoder(resp.Body)
				decoder.Decode(&root)

				type Object = map[string]interface{}
				type Array = []interface{}

				cursor := root
				cursor = cursor.(Array)[0]
				cursor = cursor.(Object)["data"]
				cursor = cursor.(Object)["video"]
				cursor = cursor.(Object)["comments"]
				cursor = cursor.(Object)["edges"]
				edges := cursor.(Array)
				for _, edge := range edges {
					cursor = edge
					cursor = cursor.(Object)["cursor"]
					gqlCursorId = cursor.(string)

					cursor = edge
					cursor = cursor.(Object)["node"]
					cursor = cursor.(Object)["contentOffsetSeconds"]
					fmt.Printf("%d,", int(cursor.(float64)))

					cursor = edge
					cursor = cursor.(Object)["node"]
					cursor = cursor.(Object)["commenter"]
					var commenter string
					if cursor != nil {
						cursor = cursor.(Object)["login"]
						commenter = cursor.(string)
					} else {
						// Apparent this may happen if the account got deleted after the stream
						commenter = "<DELETED>"
					}
					fmt.Printf("%s,", commenter)

					cursor = edge
					cursor = cursor.(Object)["node"]
					cursor = cursor.(Object)["message"]
					cursor = cursor.(Object)["userColor"]
					var color string
					if cursor != nil {
						color = cursor.(string);
					} else {
						// Taken from https://discuss.dev.twitch.com/t/default-user-color-in-chat/385
						// I don't know if it's still accurate, but I don't care, we just need some sort of
						// default color
						defaultColors := []string{
							"#FF0000", "#0000FF", "#00FF00",
							"#B22222", "#FF7F50", "#9ACD32",
							"#FF4500", "#2E8B57", "#DAA520",
							"#D2691E", "#5F9EA0", "#1E90FF",
							"#FF69B4", "#8A2BE2", "#00FF7F",
						}
						index := int(commenter[0] + commenter[len(commenter)-1])
						color = defaultColors[index % len(defaultColors)]
					}
					fmt.Printf("%s,", color)

					cursor = edge
					cursor = cursor.(Object)["node"]
					cursor = cursor.(Object)["message"]
					cursor = cursor.(Object)["fragments"]
					fragments := cursor.(Array)
					sb := strings.Builder{}
					for _, fragment := range fragments {
						cursor = fragment.(Object)["text"]
						sb.WriteString(cursor.(string))
					}
					fmt.Printf("\"%s\"\n", sb.String())
				}
				return gqlCursorId, true
			}

			fmt.Printf("%s\n", TwitchChatDownloaderCSVHeader);
			gqlCursorId, ok := queryMessagesByOffset(*videoIdPtr, "")
			if !ok {
				return false
			}
			for gqlCursorId != "" {
				gqlCursorId, ok = queryMessagesByOffset(*videoIdPtr, gqlCursorId)
				if !ok {
					return false
				}
			}
			return true
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
	fmt.Printf("FILES:\n")
	fmt.Printf("    $HOME/.markut      File that is always evaluated automatically before the MARKUT file\n");
}

func main() {
	if len(os.Args) < 2 {
		usage()
		fmt.Printf("ERROR: No subcommand is provided\n")
		os.Exit(1)
	}

	funcs = map[string]Func{
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
				context.chatLog = []ChatMessageGroup{}
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
				context.VideoCodec = &args[0]
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
				context.VideoBitrate = &args[0]
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
				context.AudioCodec = &args[0]
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
				context.AudioBitrate = &args[0]
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
				context.ExtraOutFlags = append(context.ExtraOutFlags, outFlag)
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
				context.ExtraInFlags = append(context.ExtraInFlags, inFlag)
				return true;
			},
		},
		"over": {
			Description: "Copy the argument below the top of the stack on top",
			Signature: "<a:Type1> <b:Type2> -- <a:Type1> <b:Type2> <a:Type1>",
			Category: "Stack",
			Run: func(context *EvalContext, command string, token Token) bool {
				arity := 2
				if len(context.argsStack) < arity {
					fmt.Printf("%s: Expected %d arguments but got %d", token.Loc, arity, len(context.argsStack));
					return false;
				}
				n := len(context.argsStack)
				context.argsStack = append(context.argsStack, context.argsStack[n-2]);
				return true;
			},
		},
		"dup": {
			Description: "Duplicate the argument on top of the stack",
			Signature: "<a:Type1> -- <a:Type1> <a:Type1>",
			Category: "Stack",
			Run: func(context *EvalContext, command string, token Token) bool {
				arity := 1
				if len(context.argsStack) < arity {
					fmt.Printf("%s: Expected %d arguments but got %d", token.Loc, arity, len(context.argsStack));
					return false;
				}
				n := len(context.argsStack)
				// TODO: the location of the dupped value should be the location of the "dup" token
				context.argsStack = append(context.argsStack, context.argsStack[n-1])
				return true
			},
		},
		"input": {
			Description: "Set the current input for the consequent chunks.",
			Category: "Misc",
			Signature: "<filePath:String> --",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenString)
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
				context.inputPathLog = append(context.inputPathLog, path)
				return true
			},
		},
		"chapter": {
			Description: "Define a new YouTube chapter for within a chunk for `markut summary` command.",
			Category: "Misc",
			Signature: "<timestamp:Timestamp> <title:String> --",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenString, TokenTimestamp)
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
				return true
			},
		},
		"cut": {
			Description: "Define a new cut for `markut cut` command.",
			Category: "Misc",
			Signature: "<padding:Timestamp> --",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenTimestamp)
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
				return true
			},
		},
		"include": {
			Description: "Include another MARKUT file and fail if it does not exist.",
			Category: "Misc",
			Signature: "<path:String> --",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				path := args[0]
				return context.evalMarkutFile(&path.Loc, string(path.Text), false)
			},
		},
		"include_if_exists": {
			Description: "Try to include another MARKUT file but do not fail if it does not exist.",
			Category: "Misc",
			Signature: "<path:String> --",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				path := args[0]
				return context.evalMarkutFile(&path.Loc, string(path.Text), true)
			},
		},
		"home": {
			Description: "Path to the home folder.",
			Category: "Misc",
			Signature: "-- <path:String>",
			Run: func(context *EvalContext, command string, token Token) bool {
				context.argsStack = append(context.argsStack, Token{
					Kind: TokenString,
					Text: []rune(os.Getenv("HOME")),
					Loc: token.Loc,
				})
				return true
			},
		},
		"concat": {
			Description: "Concatenate two strings.",
			Category: "Misc",
			Signature: "<a:String> <b:String> -- <a++b:String>",
			Run: func(context *EvalContext, command string, token Token) bool {
				args, err := context.typeCheckArgs(token.Loc, TokenString, TokenString)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					fmt.Printf("%s\n", err)
					return false
				}
				context.argsStack = append(context.argsStack, Token{
					Kind: TokenString,
					Text: slices.Concat(args[1].Text, args[0].Text),
					Loc: token.Loc,
				});
				return true
			},
		},
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

// TODO: Consider rewritting Markut in C with nob.h
//   There is no reason for it to be written in go at this point. C+nob.h can do all the tricks.
//   For the lexing part we can even use https://github.com/tsoding/alexer
// TODO: Embed git hash into the executable and display it on `markut version`
