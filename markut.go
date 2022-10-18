package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"path"
)

func panic_if_err(err error) {
	if err != nil {
		panic(err)
	}
}

func tsToSecs(ts string) int {
	comps := strings.Split(ts, ":")
	if len(comps) != 3 {
		panic("Expected 3 components in the timestamp")
	}

	hh, err := strconv.Atoi(comps[0])
	panic_if_err(err)
	mm, err := strconv.Atoi(comps[1])
	panic_if_err(err)
	ss, err := strconv.Atoi(comps[2])
	panic_if_err(err)

	return 60*60*hh + 60*mm + ss
}

func parseSecs(secs int) (hh int, mm int, ss int) {
	hh = secs / 60 / 60
	mm = secs / 60 % 60
	ss = secs % 60
	return
}

func secsToTs(secs int) string {
	hh, mm, ss := parseSecs(secs);
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}

// We may wanna migrate to VLC style timestamp throughout the entire codebase
func secsToVlcTs(secs int) string {
	hh, mm, ss := parseSecs(secs);
	return fmt.Sprintf("%02dH:%02dm:%02ds", hh, mm, ss)
}

type Chunk struct {
	Start   int
	End     int
	Ignored []int
	Name    string
}

func (chunk Chunk) Duration(end int) int {
	if end < chunk.Start {
		panic("Assertion Failed: Incorrect end")
	}
	return end - chunk.Start
}

func loadChunksFromFile(path string, delay int, tsFmt tsFmt) []Chunk {
	f, err := os.Open(path)
	panic_if_err(err)
	defer f.Close()

	r := csv.NewReader(f)

	var chunks []Chunk
	var chunkCurrent *Chunk = nil

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		panic_if_err(err)

		if len(record) <= 0 {
			panic("CSV record must have at least one field")
		}

		var timestamp int
		switch tsFmt {
		case TS_FMT_SECS:
			timestamp, err = strconv.Atoi(record[0])
			panic_if_err(err)
		case TS_FMT_HHMMSS:
			timestamp = tsToSecs(record[0])
		default:
			panic("unreachable")
		}
		timestamp += delay

		ignored := len(record) > 1 && record[1] == "ignore"

		if chunkCurrent == nil {
			if ignored {
				panic(fmt.Sprintf("Out of Chunk Ignored Marker %d", timestamp))
			} else {
				chunkCurrent = &Chunk{
					Start: timestamp,
				}
			}
		} else {
			if ignored {
				chunkCurrent.Ignored = append(chunkCurrent.Ignored, timestamp)
			} else {
				chunkCurrent.End = timestamp
				chunkCurrent.Name = fmt.Sprintf("chunk-%02d.mp4", len(chunks))
				chunks = append(chunks, *chunkCurrent)
				chunkCurrent = nil
			}
		}
	}

	if chunkCurrent != nil {
		panic("Unclosed chunk detected! Please make sure that there is an even amount of not ignored markers")
	}

	return chunks
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
	chunks := []string{name}
	for _, arg := range args {
		if strings.Contains(arg, " ") {
			if strings.Contains(arg, " ") {
				// TODO: use proper shell escaping instead of just wrapping with double quotes
				chunks = append(chunks, "\""+arg+"\"")
			} else {
				chunks = append(chunks, arg)
			}
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

	args = append(args, "-ss", strconv.Itoa(chunk.Start))
	args = append(args, "-i", inputPath)
	args = append(args, "-c", "copy")
	args = append(args, "-t", strconv.Itoa(chunk.Duration(chunk.End)))
	args = append(args, chunk.Name)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegConcatChunks(listPath string, outputPath string, y bool) {
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
	err := cmd.Run()
	panic_if_err(err)
}

func ffmpegGenerateConcatList(chunks []Chunk, outputPath string) {
	f, err := os.Create(outputPath)
	panic_if_err(err)
	defer f.Close()

	for _, chunk := range chunks {
		fmt.Fprintf(f, "file '%s'\n", chunk.Name)
	}
}

func usage() {
	fmt.Printf("Usage: markut <SUBCOMMAND> [OPTIONS]\n")
	fmt.Printf("SUBCOMMANDS:\n")
	fmt.Printf("    final      Render the final video\n")
	fmt.Printf("    chunk      Render specific chunk of the final video\n")
	fmt.Printf("    inspect    Inspect markers in the CSV file\n")
}

func subUsage(subFlag *flag.FlagSet) {
	fmt.Printf("Usage: markut %s [OPTIONS]\n", subFlag.Name())
	fmt.Printf("OPTIONS:\n")
	subFlag.PrintDefaults()
}

type Highlight struct {
	timestamp string
	message   string
}

func highlightChunks(chunks []Chunk) []Highlight {
	secs := 0
	highlights := []Highlight{}

	for _, chunk := range chunks {
		for _, ignored := range chunk.Ignored {
			highlights = append(highlights, Highlight{
				timestamp: secsToVlcTs(secs + chunk.Duration(ignored)),
				message:   "ignored",
			})
		}

		highlights = append(highlights, Highlight{
			timestamp: secsToVlcTs(secs + chunk.Duration(chunk.End)),
			message:   "cut",
		})

		secs += chunk.Duration(chunk.End)
	}

	return highlights
}

func finalSubcommand(args []string) {
	finalFlag := flag.NewFlagSet("final", flag.ExitOnError)
	csvPtr := finalFlag.String("csv", "", "Path to the CSV file with markers")
	inputPtr := finalFlag.String("input", "", "Path to the input video file")
	delayPtr := finalFlag.Int("delay", 0, "Delay of markers in seconds")
	yPtr := finalFlag.Bool("y", false, "Pass -y to ffmpeg")
	tsFmtNamePtr := finalFlag.String("ts-fmt", tsFmtNames[TS_FMT_SECS], "Format of the timestamps. Possible values: "+strings.Join(tsFmtNames[:], ", "))

	finalFlag.Parse(args)

	tsFmt, ok := tsFmtByName(*tsFmtNamePtr)
	if !ok {
		subUsage(finalFlag)
		fmt.Printf("ERROR: -ts-fmt: unknown timestamp format name `%s`\n", *tsFmtNamePtr)
		os.Exit(1)
	}

	if *csvPtr == "" {
		subUsage(finalFlag)
		fmt.Printf("ERROR: No -csv file is provided\n")
		os.Exit(1)
	}

	if *inputPtr == "" {
		subUsage(finalFlag)
		fmt.Printf("ERROR: No -input file is provided\n")
		os.Exit(1)
	}

	chunks := loadChunksFromFile(*csvPtr, *delayPtr, tsFmt)
	for _, chunk := range chunks {
		err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk: %s", err)
		}
	}

	ourlistPath := "ourlist.txt"
	ffmpegGenerateConcatList(chunks, ourlistPath)
	ffmpegConcatChunks(ourlistPath, "output.mp4", *yPtr)

	fmt.Println("Highlights:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("%s - %s\n", highlight.timestamp, highlight.message)
	}
}

func chunkSubcommand(args []string) {
	chunkFlag := flag.NewFlagSet("chunk", flag.ExitOnError)
	csvPtr := chunkFlag.String("csv", "", "Path to the CSV file with markers")
	inputPtr := chunkFlag.String("input", "", "Path to the input video file")
	delayPtr := chunkFlag.Int("delay", 0, "Delay of markers in seconds")
	chunkPtr := chunkFlag.Int("chunk", 0, "Chunk number to render")
	yPtr := chunkFlag.Bool("y", false, "Pass -y to ffmpeg")
	tsFmtNamePtr := chunkFlag.String("ts-fmt", tsFmtNames[TS_FMT_SECS], "Format of the timestamps. Possible values: "+strings.Join(tsFmtNames[:], ", "))

	chunkFlag.Parse(args)

	tsFmt, ok := tsFmtByName(*tsFmtNamePtr)
	if !ok {
		subUsage(chunkFlag)
		fmt.Printf("ERROR: -ts-fmt: unknown timestamp format name `%s`\n", *tsFmtNamePtr)
		os.Exit(1)
	}

	if *csvPtr == "" {
		subUsage(chunkFlag)
		fmt.Printf("ERROR: No -csv file is provided\n")
		os.Exit(1)
	}

	if *inputPtr == "" {
		subUsage(chunkFlag)
		fmt.Printf("ERROR: No -input file is provided\n")
		os.Exit(1)
	}

	chunks := loadChunksFromFile(*csvPtr, *delayPtr, tsFmt)

	if *chunkPtr > len(chunks) {
		fmt.Printf("ERROR: %d is incorrect chunk number. There is only %d of them.\n", *chunkPtr, len(chunks))
		os.Exit(1)
	}

	chunk := chunks[*chunkPtr]

	err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
	panic_if_err(err)

	fmt.Printf("%s is rendered!\n", chunk.Name)
	if len(chunk.Ignored) > 0 {
		fmt.Printf("Ignored timestamps:\n")
		for _, ignored := range chunk.Ignored {
			fmt.Printf("  %s\n", secsToTs(chunk.Duration(ignored)))
		}
	}
}

type tsFmt = int
const (
	TS_FMT_SECS=iota
	TS_FMT_HHMMSS=iota
	COUNT_TS_FMTS=iota
)

var tsFmtNames = [COUNT_TS_FMTS]string{
	TS_FMT_SECS: "secs",
	TS_FMT_HHMMSS: "hhmmss",
}

func tsFmtByName(needle string) (tsFmt, bool) {
	for index, name := range tsFmtNames {
		if needle == name {
			return index, true
		}
	}
	return 0, false
}

func inspectSubcommand(args []string) {
	inspectFlag := flag.NewFlagSet("inspect", flag.ExitOnError)
	csvPtr := inspectFlag.String("csv", "", "Path to the CSV file with markers")
	delayPtr := inspectFlag.Int("delay", 0, "Delay of markers in seconds")
	tsFmtNamePtr := inspectFlag.String("ts-fmt", tsFmtNames[TS_FMT_SECS], "Format of the timestamps. Possible values: "+strings.Join(tsFmtNames[:], ", "))

	inspectFlag.Parse(args)

	tsFmt, ok := tsFmtByName(*tsFmtNamePtr)
	if !ok {
		subUsage(inspectFlag)
		fmt.Printf("ERROR: -ts-fmt: unknown timestamp format name `%s`\n", *tsFmtNamePtr)
		os.Exit(1)
	}

	if *csvPtr == "" {
		subUsage(inspectFlag)
		fmt.Printf("ERROR: No -csv file is provided\n")
		os.Exit(1)
	}

	chunks := loadChunksFromFile(*csvPtr, *delayPtr, tsFmt)
	fmt.Println("Chunks:")
	for _, chunk := range chunks {
		fmt.Printf("  Name:  %s\n", chunk.Name)
		fmt.Printf("  Start: %s (%d)\n", secsToTs(chunk.Start), chunk.Start)
		fmt.Printf("  End:   %s (%d)\n", secsToTs(chunk.End), chunk.End)
		fmt.Printf("  Ignored:\n")
		for _, ignored := range chunk.Ignored {
			fmt.Printf("    %s (%d)\n", secsToTs(ignored), ignored)
		}
		fmt.Printf("\n")
	}

	fmt.Println("Cuts:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("  %s - %s\n", highlight.timestamp, highlight.message)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		fmt.Printf("ERROR: No subcommand is provided\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "final":
		finalSubcommand(os.Args[2:])
	case "chunk":
		chunkSubcommand(os.Args[2:])
	case "inspect":
		inspectSubcommand(os.Args[2:])
	default:
		usage()
		fmt.Printf("Unknown subcommand %s\n", os.Args[1])
		os.Exit(1)
	}
}
