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
	Chapters []Chapter
}

func (chunk Chunk) Duration() Secs {
	return chunk.End - chunk.Start
}

type Chapter struct {
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

func loadChunksFromMarkutFile(path string, delay Secs) (chunks []Chunk, err error) {
	var content []byte
	content, err = os.ReadFile(path)
	if err != nil {
		return
	}

	lexer := NewLexer(string(content), path)
	var token Token
	var argsStack []Token
	var chapStack []Chapter
	for {
		token, err = lexer.Next()
		if err != nil {
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
				// TODO: can we use wrapped errors in here?
				fmt.Printf("%s: ERROR: type check failed for subtraction\n", token.Loc)
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
			case "chapter":
				fallthrough
			case "timestamp":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenString, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					return
				}
				chapStack = append(chapStack, Chapter{
					Label: string(args[0].Text),
					Timestamp: args[1].Timestamp,
				})
			case "chunk":
				args, err, argsStack = typeCheckArgs(token.Loc, argsStack, TokenTimestamp, TokenTimestamp)
				if err != nil {
					fmt.Printf("%s: ERROR: type check failed for %s\n", token.Loc, command)
					return
				}

				chunks = append(chunks, Chunk{
					Start: args[1].Timestamp,
					End: args[0].Timestamp,
					// TODO: if the name of the chunk is its number, why do we need to store it?
					// We can just compute it when we need it, can we?
					Name: fmt.Sprintf("chunk-%02d.mp4", len(chunks)),
					Chapters: chapStack,
				})

				chapStack = []Chapter{}
			default:
				err = &DiagErr{
					Loc: token.Loc,
					Err: fmt.Errorf("Unknown command %s", command),
				}
				return
			}
		default:
			err = &DiagErr{
				Loc: token.Loc,
				Err: fmt.Errorf("Unexpected token %s", TokenKindName[token.Kind]),
			}
			return
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

func ffmpegCutChunk(inputPath string, chunk Chunk, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	args = append(args, "-ss", strconv.FormatFloat(chunk.Start, 'f', -1, 64))
	args = append(args, "-i", inputPath)
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

func finalSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("final", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, *delayPtr)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	listPath := "final-list.txt"
	err = ffmpegGenerateConcatList(chunks, listPath)
	if err != nil {
		return fmt.Errorf("Could not generate final concat list %s: %w", listPath, err)
	}

	outputPath := "output.mp4"
	err = ffmpegConcatChunks(listPath, outputPath, *yPtr)
	if err != nil {
		return fmt.Errorf("Could not generated final output %s: %w", outputPath, err)
	}

	fmt.Println("Highlights:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("%s - %s\n", highlight.timestamp, highlight.message)
	}

	return nil
}

func cutSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("cut", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	cutPtr := subFlag.Int("cut", 0, "Cut number to render")
	padPtr := subFlag.String("pad", "00:00:02", "Amount of time to pad around the cut (supports the markut's timestamp format)")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	pad, err := tsToSecs(*padPtr)
	if err != nil {
		subFlag.Usage()
		return fmt.Errorf("%s is not a correct timestamp for -pad: %w", *padPtr, err)
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, *delayPtr)
	if err != nil {
		return err
	}

	if *cutPtr+1 >= len(chunks) {
		return fmt.Errorf("%d is an invalid cut number. There is only %d of them.", *cutPtr, len(chunks)-1)
	}

	cutChunks := []Chunk{
		{
			Start: chunks[*cutPtr].End - pad,
			End:   chunks[*cutPtr].End,
			Name:  fmt.Sprintf("cut-%02d-left.mp4", *cutPtr),
		},
		{
			Start: chunks[*cutPtr+1].Start,
			End:   chunks[*cutPtr+1].Start + pad,
			Name:  fmt.Sprintf("cut-%02d-right.mp4", *cutPtr),
		},
	}

	for _, chunk := range cutChunks {
		err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	cutListPath := "cut-%02d-list.txt"
	listPath := fmt.Sprintf(cutListPath, *cutPtr)
	err = ffmpegGenerateConcatList(cutChunks, listPath)
	if err != nil {
		return fmt.Errorf("Could not generate not generate cut concat list %s: %w", cutListPath, err)
	}

	cutOutputPath := "cut-%02d.mp4"
	err = ffmpegConcatChunks(listPath, fmt.Sprintf(cutOutputPath, *cutPtr), *yPtr)
	if err != nil {
		return fmt.Errorf("Could not generate cut output file %s: %w", cutOutputPath, err)
	}

	return nil
}

func chaptersSubcommand(args []string) error {
	chapFlag := flag.NewFlagSet("chapters", flag.ContinueOnError)
	markutPtr := chapFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := chapFlag.Parse(args)

	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		chapFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, 0)
	if err != nil {
		return err
	}

	var offset Secs = 0;
	for _, chunk := range chunks {
		for _, chapter := range chunk.Chapters {
			fmt.Printf("%s - %s\n", secsToTs(int(math.Floor(chapter.Timestamp - chunk.Start + offset))), chapter.Label);
		}
		offset += chunk.End - chunk.Start;
	}

	return nil
}

func chunkSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("chunk", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)

	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, *delayPtr)
	if err != nil {
		return err
	}

	if *chunkPtr > len(chunks) {
		return fmt.Errorf("%d is an incorrect chunk number. There is only %d of them.", *chunkPtr, len(chunks))
	}

	chunk := chunks[*chunkPtr]

	err = ffmpegCutChunk(*inputPtr, chunk, *yPtr)
	if err != nil {
		return fmt.Errorf("Could not cut the chunk %s: %s", chunk.Name, err)
	}

	fmt.Printf("%s is rendered!\n", chunk.Name)
	return nil
}

func inspectSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("inspect", flag.ContinueOnError)
	markutPtr := subFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, *delayPtr)
	if err != nil {
		return err
	}
	fmt.Println("Chunks:")
	for _, chunk := range chunks {
		fmt.Printf("  Name:  %s\n", chunk.Name)
		fmt.Printf("  Start: %s (%s)\n", secsToTs(int(chunk.Start)), strconv.FormatFloat(chunk.Start, 'f', -1, 64))
		fmt.Printf("  End:   %s (%s)\n", secsToTs(int(chunk.End)), strconv.FormatFloat(chunk.End, 'f', -1, 64))
	}

	fmt.Println("Cuts:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("  %s - %s\n", highlight.timestamp, highlight.message)
	}
	return nil
}

func fixupSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("fixup", flag.ExitOnError)
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	outputPath := "input.ts"
	err = ffmpegFixupInput(*inputPtr, outputPath, *yPtr)
	if err != nil {
		return fmt.Errorf("Could not fixup input file %s: %w", *inputPtr, err)
	}
	fmt.Printf("Generated %s\n", outputPath)

	return nil
}

type Subcommand struct {
	Name        string
	Run         func(args []string) error
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
	// TODO: we probably want to remove inspect subcommand
	{
		Name:        "inspect",
		Run:         inspectSubcommand,
		Description: "Inspect markers in the Markut file",
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
			err := subcommand.Run(os.Args[2:])
			if err != nil {
				fmt.Printf("%s\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	usage()
	fmt.Printf("ERROR: Unknown subcommand %s\n", os.Args[1])
	os.Exit(1)
}
