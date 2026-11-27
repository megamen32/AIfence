#!/usr/bin/env python3
"""Compare a silent Playwright source and its complete MP4 without modifying either file."""
import argparse
import bisect
import collections
import hashlib
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
from pathlib import Path

WIDTH, HEIGHT = 48, 27
FRAME_BYTES = WIDTH * HEIGHT

def executable(requested):
    options = [requested] if requested else [
        os.environ.get("AIFENCE_FFMPEG"), os.environ.get("FFMPEG"), os.environ.get("IMAGEIO_FFMPEG_EXE"),
        shutil.which("ffmpeg"), "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg",
    ]
    if not requested:
        try:
            import imageio_ffmpeg
            options.append(imageio_ffmpeg.get_ffmpeg_exe())
        except (ImportError, RuntimeError):
            pass
    if not requested and sys.platform == "darwin":
        options.extend(str(p) for p in (Path.home()/".cache/uv/archive-v0").glob("*/lib/python*/site-packages/imageio_ffmpeg/binaries/ffmpeg-macos-*"))
    for option in filter(None, options):
        try:
            if subprocess.run([option, "-version"], stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL, timeout=8).returncode == 0:
                return option
        except (OSError, subprocess.TimeoutExpired):
            continue
    raise ValueError("No working FFmpeg. Pass --ffmpeg /path/to/ffmpeg.")

def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()

def decode(exe, path):
    args = [exe, "-hide_banner", "-nostdin", "-nostats", "-i", str(path),
            "-map", "0:v:0", "-an", "-vf", f"scale={WIDTH}:{HEIGHT}:flags=area,format=gray,showinfo",
            "-fps_mode", "passthrough", "-threads", "1", "-f", "rawvideo", "pipe:1"]
    p = subprocess.run(args, capture_output=True, timeout=180)
    log = p.stderr.decode("utf-8", "replace")
    if p.returncode:
        raise ValueError(f"{path.name} failed decoding: {log[-380:]}")
    src = log.split("Stream mapping:", 1)[0]
    streams = re.findall(r"^\s*Stream #0:\d+[^\n]*?: (Video|Audio):", src, re.M)
    if streams.count("Video") != 1 or "Audio" in streams:
        raise ValueError(f"{path.name} must contain one silent video stream. Found {streams}.")
    times = [float(s) for s in re.findall(r"\bn:\s*\d+\s+pts:\s*-?\d+\s+pts_time:\s*([\d.eE+-]+)", log)]
    if len(times) < 2 or len(p.stdout) != len(times)*FRAME_BYTES:
        raise ValueError(f"{path.name}: missing frames or timestamps.")
    gaps = [b-a for a,b in zip(times,times[1:])]
    if min(gaps) <= 0:
        raise ValueError(f"{path.name}: nonmonotonic presentation timestamps.")
    dt = statistics.median(gaps)
    m = re.search(r"Duration:\s*(\d+):(\d+):([0-9.]+)", src)
    duration = int(m[1])*3600 + int(m[2])*60 + float(m[3]) if m else times[-1]-times[0]+dt
    if not (0 < duration <= 180.0001):
        raise ValueError(f"{path.name}: duration {duration:.3f}s exceeds the take budget.")
    return times, p.stdout, {"name":path.name,"sha256":digest(path),"duration_s":round(duration,4),
        "frames":len(times),"first_pts_s":times[0],"median_step_s":round(dt,5),
        "largest_pts_gap_s":round(max(gaps),5),"audio_streams":0}

def check_receipt(path, raw, mp4):
    entry=json.loads(path.read_text())
    if entry.get("status")!="complete":
        raise ValueError("Recorder reports an incomplete take.")
    if not isinstance(entry.get("mode"),str) or not entry["mode"]:
        raise ValueError("Recording mode is missing.")
    if entry.get("mode")=="fresh" and not entry.get("conversation_id"):
        raise ValueError("Fresh take has no linked conversation in its receipt.")
    if entry.get("mode")=="review" and not any(entry.get(k) for k in ("hypothesis_id","watch_id","native_card_url","card_url","source_url","case_url")):
        raise ValueError("Review take has no linked native case in its receipt.")
    for value in (raw,mp4):
        claimed=entry.get("files",{}).get(value["name"],{}).get("sha256")
        if claimed!=value["sha256"]:
            raise ValueError(f"Receipt SHA-256 mismatch for {value['name']}.")
    trace=entry.get("events",[])
    if not trace or not all(isinstance(v.get("action"),str) and isinstance(v.get("t"),(float,int)) for v in trace):
        raise ValueError("Missing timed event journal.")
    if any(b["t"]<a["t"] for a,b in zip(trace,trace[1:])):
        raise ValueError("Recorder clock moves backwards in event journal.")
    return {"mode":entry["mode"],"event_count":len(trace),"declared_model_answer_verified":entry.get("model_answer_verified")}

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("raw", type=Path)
    ap.add_argument("mp4", type=Path)
    ap.add_argument("--ffmpeg")
    ap.add_argument("--receipt",type=Path,help="Verify the recorder hashes and the monotonic action journal.")
    ap.add_argument("--json",type=Path,help="Also write the machine-readable verification report.")
    a=ap.parse_args()
    exe=executable(a.ffmpeg)
    ta,ra,ma=decode(exe,a.raw)
    tb,rb,mb=decode(exe,a.mp4)
    tolerance=max(ma["median_step_s"],mb["median_step_s"])*1.1+.011
    if abs(ma["duration_s"]-mb["duration_s"])>tolerance:
        raise ValueError("Source and export have different durations.")
    aa=[t-ta[0] for t in ta]
    covered=collections.Counter()
    worst=0.0
    total=0.0
    for j, pts in enumerate(tb):
        t=pts-tb[0]
        i=bisect.bisect_left(aa,t)
        candidate=min((k for k in [i-1,i] if 0<=k<len(aa)), key=lambda k:abs(aa[k]-t))
        covered[candidate]+=1
        if abs(aa[candidate]-t)>tolerance:
            raise ValueError(f"No matching source timestamp at {t:.3f}s.")
        x=ra[candidate*FRAME_BYTES:(candidate+1)*FRAME_BYTES]
        y=rb[j*FRAME_BYTES:(j+1)*FRAME_BYTES]
        mae=sum(abs(v-w) for v,w in zip(x,y))/FRAME_BYTES
        worst=max(worst,mae)
        total+=mae
    if len(ta)==len(tb) and len(covered)!=len(ta):
        raise ValueError("Matching timestamps skip source frames.")
    if worst>10 or total/len(tb)>2.5:
        raise ValueError(f"Source/MP4 images differ. Peak MAE {worst:.3f}, mean {total/len(tb):.3f}.")
    report={"raw":ma,"mp4":mb,"raw_frames_matched":len(covered),"mean_luma_error_0_255":round(total/len(tb),4),
            "max_frame_luma_error_0_255":round(worst,4),
            "method":"Full decode, presentation timestamps, 48x27 luma fingerprint per output frame. Read alongside the recorder and its event log."}
    if a.receipt:
        report["receipt"]=check_receipt(a.receipt,ma,mb)
    data=json.dumps(report,ensure_ascii=False,indent=2)
    if a.json:
        a.json.write_text(data+"\n")
    print(data)

if __name__=="__main__":
    try:
        main()
    except (OSError, subprocess.SubprocessError, ValueError) as e:
        sys.exit(str(e))
