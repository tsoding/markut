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

type Secs = float64

func tsToSecs(ts string) (Secs, error) {
	var err error = nil
	var mm, hh int = 0, 0
	var ss Secs = 0
	var index = 0
	switch comps := strings.Split(ts, ":"); len(comps) {
	case 3:
		hh, err = strconv.Atoi(comps[index])
		if err != nil {
			return 0, err
		}
		index += 1
		fallthrough
	case 2:
		mm, err = strconv.Atoi(comps[index])
		if err != nil {
			return 0, err
		}
		index += 1
		fallthrough
	case 1:
		ss, err = strconv.ParseFloat(comps[index], 64)
		if err != nil {
			return 0, err
		}
		return 60*60*Secs(hh) + 60*Secs(mm) + ss, nil
	default:
		return 0, fmt.Errorf("Unexpected amount of components in the timestamp (%d)", len(comps))
	}
}

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
	Timestamps []Timestamp
}

func (chunk Chunk) Duration() Secs {
	return chunk.End - chunk.Start
}

type Timestamp struct {
	Label string
	Time Secs
}

func loadChunksFromMarkutFile(path string, delay Secs) (chunks []Chunk, err error) {
	var content []byte
	content, err = os.ReadFile(path)
	if err != nil {
		return
	}

	lexer := NewLexer(string(content), path)
	var token Token
	var stack []Token
	var timestamps []Timestamp
	for {
		token, err = lexer.Next()
		if err != nil {
			return
		}

		if token.Kind == TokenEOF {
			break
		}

		switch token.Kind {
		case TokenDash:
			n := len(stack)
			if n < 2 {
				err = &DiagErr{
					Loc: token.Loc,
					Err: fmt.Errorf("Not enough timestamps to subtract. Expected 2 but got %d", n),
				}
				return
			}
			if stack[n-1].Kind != TokenTimestamp {
				err = &DiagErr{
					Loc: stack[n-1].Loc,
					Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-1].Kind]),
				}
				return
			}
			if stack[n-2].Kind != TokenTimestamp {
				err = &DiagErr{
					Loc: stack[n-2].Loc,
					Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-2].Kind]),
				}
				return
			}
			stack[n-2].Timestamp -= stack[n-1].Timestamp
			stack = stack[:n-1]
		case TokenPlus:
			n := len(stack)
			if n < 2 {
				err = &DiagErr{
					Loc: token.Loc,
					Err: fmt.Errorf("Not enough timestamps to sum up. Expected 2 but got %d", n),
				}
				return
			}
			if stack[n-1].Kind != TokenTimestamp {
				err = &DiagErr{
					Loc: stack[n-1].Loc,
					Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-1].Kind]),
				}
				return
			}
			if stack[n-2].Kind != TokenTimestamp {
				err = &DiagErr{
					Loc: stack[n-2].Loc,
					Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-2].Kind]),
				}
				return
			}
			stack[n-2].Timestamp += stack[n-1].Timestamp
			stack = stack[:n-1]
		case TokenString:
			fallthrough
		case TokenTimestamp:
			stack = append(stack, token)
		case TokenSymbol:
			command := string(token.Text)
			switch command {
			case "timestamp":
				// TODO: implement proper timestamp handling
				n := len(stack)
				if n < 2 {
					err = &DiagErr{
						Loc: token.Loc,
						Err: fmt.Errorf("Not enough arguments for command %s. Expected 2 but got %d", command, n),
					}
					return
				}
				if stack[n-1].Kind != TokenString {
					err = &DiagErr{
						Loc: stack[n-1].Loc,
						Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenString], TokenKindName[stack[n-1].Kind]),
					}
					return
				}
				if stack[n-2].Kind != TokenTimestamp {
					err = &DiagErr{
						Loc: stack[n-2].Loc,
						Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-2].Kind]),
					}
					return
				}

				timestamps = append(timestamps, Timestamp{
					Label: string(stack[n-1].Text),
					Time: stack[n-2].Timestamp,
				})

				stack = stack[:n-2];
			case "chunk":
				n := len(stack)
				if n < 2 {
					err = &DiagErr{
						Loc: token.Loc,
						Err: fmt.Errorf("Not enough arguments for command %s. Expected 2 but got %d", command, n),
					}
					return
				}
				if stack[n-1].Kind != TokenTimestamp {
					err = &DiagErr{
						Loc: stack[n-1].Loc,
						Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-1].Kind]),
					}
					return
				}
				if stack[n-2].Kind != TokenTimestamp {
					err = &DiagErr{
						Loc: stack[n-2].Loc,
						Err: fmt.Errorf("Expected %s but got %s\n", TokenKindName[TokenTimestamp], TokenKindName[stack[n-2].Kind]),
					}
					return
				}

				chunks = append(chunks, Chunk{
					Start: stack[n-2].Timestamp,
					End: stack[n-1].Timestamp,
					Name: fmt.Sprintf("chunk-%02d.mp4", len(chunks)),
					Timestamps: timestamps,
				})
				timestamps = []Timestamp{}
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
		return fmt.Errorf("Could not load chunks from file %s: %w", *markutPtr, err)
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
		return fmt.Errorf("Could not load chunks from file %s: %w", *markutPtr, err)
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

func timestampsSubcommand(args []string) error {
	tsFlag := flag.NewFlagSet("timestamps", flag.ContinueOnError)
	markutPtr := tsFlag.String("markut", "", "Path to the Markut file with markers (mandatory)")

	err := tsFlag.Parse(args)

	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *markutPtr == "" {
		tsFlag.Usage()
		return fmt.Errorf("No -markut file is provided")
	}

	chunks, err := loadChunksFromMarkutFile(*markutPtr, 0)
	if err != nil {
		return fmt.Errorf("Could not load chunks from file %s: %w", *markutPtr, err)
	}

	var offset Secs = 0;
	for _, chunk := range chunks {
		for _, timestamp := range chunk.Timestamps {
			fmt.Printf("%s - %s\n", secsToTs(int(math.Floor(timestamp.Time - chunk.Start + offset))), timestamp.Label);
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
		return fmt.Errorf("Could not load chunks from file %s: %w", *markutPtr, err)
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
		return fmt.Errorf("Could not load chunks from file %s: %w", *markutPtr, err)
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
		Name: "timestamps",
		Run: timestampsSubcommand,
		Description: "Generate YouTube timestamps",
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
				fmt.Printf("ERROR: %s\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	usage()
	fmt.Printf("ERROR: Unknown subcommand %s\n", os.Args[1])
	os.Exit(1)
}
