#!/usr/bin/env python3

import csv
import sys
import subprocess
import argparse

from typing import List, NewType, Iterable

Secs = NewType('Secs', int)

def ts_to_secs(ts: str) -> Secs:
    comps = ts.split(':');
    assert len(comps) == 3;
    return Secs(60 * 60 * int(comps[0]) + 60 * int(comps[1]) + int(comps[2]));

def secs_to_ts(secs: int) -> str:
    return f'{secs//60//60:02}:{secs//60%60:02}:{secs%60:02}';

def ffmpeg_cut_chunk(input_path: str,
                     start_secs: int,
                     duration_secs: int,
                     output_path: str) -> None:
    cli = ['ffmpeg',
           '-ss', str(start_secs),
           '-i', input_path,
           '-c', 'copy',
           '-t', str(duration_secs),
           output_path];
    subprocess.run(cli)

def ffmpeg_concat_chunks(list_path: str, output_path: str) -> None:
    cli = ['ffmpeg',
           '-f', 'concat',
           '-safe', '0',
           '-i', list_path,
           '-c', 'copy',
           output_path]
    subprocess.run(cli)

def ffmpeg_generate_concat_list(chunk_names: Iterable[str], output_path: str) -> None:
    with open(output_path, 'w') as f:
        for name in chunk_names:
            f.write(f"file '{name}'\n")

def load_ts_from_file(path: str, delay: int) -> List[int]:
    with open(path, newline='') as csvfile:
        return [ts_to_secs(row[0]) + delay
                for row in csv.reader(csvfile)];


if __name__ == '__main__':
    parser = argparse.ArgumentParser();
    parser.add_argument('-c', dest='csv', required=True, metavar='CSV');
    parser.add_argument('-i', dest='input', required=True, metavar='INPUT');
    parser.add_argument('-d', dest='delay', required=True, metavar='DELAY_SECS');
    args = parser.parse_args();
    ts = load_ts_from_file(args.csv, int(args.delay));
    n = len(ts)
    assert n % 2 == 0

    secs = 0;
    cuts_ts: List[str] = [];
    for i in range(0, n // 2):
        start    = ts[i * 2 + 0]
        end      = ts[i * 2 + 1]
        duration = end - start
        secs    += duration
        cuts_ts.append(secs_to_ts(secs));
        ffmpeg_cut_chunk(args.input, start, duration, f'chunk-{i:02}.mp4')

    ourlist_path = 'ourlist.txt'
    chunk_names = [f'chunk-{i:02}.mp4' for i in range(0, n // 2)]
    ffmpeg_generate_concat_list(chunk_names, ourlist_path);
    ffmpeg_concat_chunks(ourlist_path, "output.mp4")

    print("Timestamps of cuts:");
    for cut in cuts_ts:
        print(cut)
