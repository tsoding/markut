// TODO: angled brackets are not allowed on YouTube. Let's make `chapters` check for that.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"math"
)

// TODO: Make secsToTs accept float instead of int
func secsToTs(secs int) string {
	hh := secs / 60 / 60
	mm := secs / 60 % 60
	ss := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}

type Chunk struct {
	Start Secs
	End   Secs
	Name  string
	Loc Loc
	InputPath string
}

func (chunk Chunk) Duration() Secs {
	return chunk.End - chunk.Start
}

type Chapter struct {
	Loc Loc
	Timestamp Secs
	Label string
}

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

const MinYouTubeChapterDuration Secs = 10.0

type EvalContext struct {
	inputPath string
	chunks []Chunk
	chapters []Chapter
}

func evalMarkutFile(path string) (context EvalContext, ok bool) {
	ok = true
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("ERROR: could not read file %s: %s\n", path, err)
		ok = false
		return
	}

	lexer := NewLexer(string(content), path)
	token := Token{}
	argsStack := []Token{}
	chapStack := []Chapter{}
	chapOffset := 0.0
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
				argsStack = append(argsStack, argsStack[n-1])
			case "chapter":
				fallthrough
			case "timestamp":
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

				if start.Timestamp > end.Timestamp {
					fmt.Printf("%s: ERROR: the end of the chunk %s is earlier than its start %s\n", end.Loc, secsToTs(int(math.Floor(end.Timestamp))), secsToTs(int(math.Floor(start.Timestamp))));
					fmt.Printf("%s: NOTE: the start is located here\n", start.Loc);
					ok = false
					return
				}

				chunk := Chunk{
					Loc: token.Loc,
					Start: start.Timestamp,
					End: end.Timestamp,
					// TODO: if the name of the chunk is its number, why do we need to store it?
					// We can just compute it when we need it, can we?
					Name: fmt.Sprintf("chunk-%02d.mp4", len(context.chunks)),
					InputPath: context.inputPath,
				}

				context.chunks = append(context.chunks, chunk)

				for _, chapter := range chapStack {
					if chapter.Timestamp < chunk.Start || chunk.End < chapter.Timestamp {
						fmt.Printf("%s: ERROR: the timestamp %s of chapter \"%s\" is outside of the the current chunk\n", chapter.Loc, secsToTs(int(math.Floor(chapter.Timestamp))), chapter.Label)
						fmt.Printf("%s: NOTE: which starts at %s\n", start.Loc, secsToTs(int(math.Floor(start.Timestamp))))
						fmt.Printf("%s: NOTE: and ends at %s\n", end.Loc, secsToTs(int(math.Floor(end.Timestamp))))
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
			fmt.Printf("%s: ERROR: Unexpected token %s\n", TokenKindName[token.Kind]);
			ok = false;
			return
		}
	}

	for i := 0; i + 1 < len(context.chapters); i += 1 {
		duration := context.chapters[i + 1].Timestamp - context.chapters[i].Timestamp;
		if duration < MinYouTubeChapterDuration {
			fmt.Printf("%s: ERROR: the chapter \"%s\" has duration %s which is shorter than the minimal allowed YouTube chapter duration which is %s (See https://support.google.com/youtube/answer/9884579)\n", context.chapters[i].Loc, context.chapters[i].Label, secsToTs(int(math.Floor(duration))), secsToTs(int(math.Floor(MinYouTubeChapterDuration))));
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
		for i := range argsStack {
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

func ffmpegCutChunk(chunk Chunk, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	args = append(args, "-ss", strconv.FormatFloat(chunk.Start, 'f', -1, 64))
	args = append(args, "-i", chunk.InputPath)
	args = append(args, "-c", "copy")
	args = append(args, "-t", strconv.FormatFloat(chunk.Duration(), 'f', -1, 64))
	args = append(args, chunk.Name)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegConcatChunks(listPath string, outputPath string, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

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
		fmt.Fprintf(f, "file '%s'\n", chunk.Name)
	}

	return nil
}

type Highlight struct {
	timestamp string
	message   string
}

func highlightChunks(chunks []Chunk) []Highlight {
	secs := 0.0
	highlights := []Highlight{}

	for _, chunk := range chunks {
		highlights = append(highlights, Highlight{
			timestamp: secsToTs(int(secs + chunk.Duration())),
			message:   "cut",
		})

		secs += chunk.Duration()
	}

	return highlights
}

func finalSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("final", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

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

	if context.inputPath == "" {
		fmt.Printf("ERROR: No input file is provided. Use `input` command in markut file\n")
		return false
	}

	for _, chunk := range context.chunks {
		err := ffmpegCutChunk(chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	listPath := "final-list.txt"
	err = ffmpegGenerateConcatList(context.chunks, listPath)
	if err != nil {
		fmt.Printf("ERROR: Could not generate final concat list %s: %s\n", listPath, err)
		return false;
	}

	outputPath := "output.mp4"
	err = ffmpegConcatChunks(listPath, outputPath, *yPtr)
	if err != nil {
		fmt.Printf("ERROR: Could not generated final output %s: %s\n", outputPath, err)
		return false
	}

	// TODO: maybe these should be called cuts?
	fmt.Println("Highlights:")
	for _, highlight := range highlightChunks(context.chunks) {
		fmt.Printf("%s - %s\n", highlight.timestamp, highlight.message)
	}
	fmt.Println()
	fmt.Println("Chapters:")
	for _, chapter := range context.chapters {
		fmt.Printf("- %s - %s\n", secsToTs(int(math.Floor(chapter.Timestamp))), chapter.Label)
	}

	return true
}

func cutSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("cut", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	cutPtr := subFlag.Int("cut", 0, "Cut number to render")
	padPtr := subFlag.String("pad", "00:00:02", "Amount of time to pad around the cut (supports the markut's timestamp format)")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

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

	pad, err := tsToSecs(*padPtr)
	if err != nil {
		subFlag.Usage()
		fmt.Printf("ERROR: %s is not a correct timestamp for -pad: %s\n", *padPtr, err)
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
	if !ok {
		return false
	}

	if context.inputPath == "" {
		fmt.Printf("ERROR: No input file is provided. Use `input` command in markut file\n")
		return false
	}

	if *cutPtr+1 >= len(context.chunks) {
		fmt.Printf("ERROR: %d is an invalid cut number. There is only %d of them.\n", *cutPtr, len(context.chunks)-1)
		return false
	}

	cutChunks := []Chunk{
		{
			Start: context.chunks[*cutPtr].End - pad,
			End:   context.chunks[*cutPtr].End,
			Name:  fmt.Sprintf("cut-%02d-left.mp4", *cutPtr),
			InputPath: context.chunks[*cutPtr].InputPath,
		},
		{
			Start: context.chunks[*cutPtr+1].Start,
			End:   context.chunks[*cutPtr+1].Start + pad,
			Name:  fmt.Sprintf("cut-%02d-right.mp4", *cutPtr),
			InputPath: context.chunks[*cutPtr+1].InputPath,
		},
	}

	for _, chunk := range cutChunks {
		err := ffmpegCutChunk(chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	cutListPath := "cut-%02d-list.txt"
	listPath := fmt.Sprintf(cutListPath, *cutPtr)
	err = ffmpegGenerateConcatList(cutChunks, listPath)
	if err != nil {
		fmt.Printf("ERROR: Could not generate not generate cut concat list %s: %s\n", cutListPath, err)
		return false
	}

	cutOutputPath := "cut-%02d.mp4"
	err = ffmpegConcatChunks(listPath, fmt.Sprintf(cutOutputPath, *cutPtr), *yPtr)
	if err != nil {
		fmt.Printf("ERROR: Could not generate cut output file %s: %s\n", cutOutputPath, err)
		return false
	}

	fmt.Printf("%s: NOTE: cut is defined in here\n", context.chunks[*cutPtr].Loc);

	return true
}

func chaptersSubcommand(args []string) bool {
	chapFlag := flag.NewFlagSet("chapters", flag.ContinueOnError)
	markutPtr := chapFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := chapFlag.Parse(args)

	if err == flag.ErrHelp {
		return true
	}

	if err != nil {
		fmt.Printf("ERROR: Could not parse command line arguments: %s\n", err);
		return false
	}

	if *markutPtr == "" {
		chapFlag.Usage()
		fmt.Printf("ERROR: No -markut file is provided\n");
		return false
	}

	context, ok := evalMarkutFile(*markutPtr)
	if !ok {
		return false
	}

	fmt.Println("Chapters:")
	for _, chapter := range context.chapters {
		fmt.Printf("- %s - %s\n", secsToTs(int(math.Floor(chapter.Timestamp))), chapter.Label);
	}

	return true
}

func chunkSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("chunk", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

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

	if context.inputPath == "" {
		fmt.Printf("ERROR: No input file is provided. Use `input` command in markut file\n")
		return false
	}

	if *chunkPtr > len(context.chunks) {
		fmt.Printf("ERROR: %d is an incorrect chunk number. There is only %d of them.\n", *chunkPtr, len(context.chunks))
		return false
	}

	chunk := context.chunks[*chunkPtr]

	err = ffmpegCutChunk(chunk, *yPtr)
	if err != nil {
		fmt.Printf("ERROR: Could not cut the chunk %s: %s\n", chunk.Name, err)
		return false
	}

	fmt.Printf("%s is rendered!\n", chunk.Name)
	return true
}

func fixupSubcommand(args []string) bool {
	subFlag := flag.NewFlagSet("fixup", flag.ExitOnError)
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
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

	outputPath := "input.ts"
	err = ffmpegFixupInput(*inputPtr, outputPath, *yPtr)
	if err != nil {
		fmt.Printf("ERROR: Could not fixup input file %s: %s\n", *inputPtr, err)
		return false
	}
	fmt.Printf("Generated %s\n", outputPath)

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
		Name: "chapters",
		Run: chaptersSubcommand,
		Description: "Generate YouTube chapters",
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
