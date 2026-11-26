#!/usr/bin/env python3
"""Build the two competitor collectors and the joined CSV exporter on this Mac."""
import os
import pathlib
import plistlib
import shutil
import subprocess

collector = pathlib.Path(__file__).resolve().parent
project = collector.parents[2]
app = collector.parent
runtime = project / ".runtime"
data = runtime / "market-catalog"
binaries = runtime / "bin"
labels = pathlib.Path.home() / "Library" / "LaunchAgents"
for path in (data, binaries, labels):
    path.mkdir(parents=True, exist_ok=True)

go = shutil.which("go") or "/usr/local/go/bin/go"
for cmd, binary in (("fullcatalog","fullcatalog"), ("catalogexport","catalogexport"),
                    ("softlinefull","softlinefull"), ("coverage","market-coverage")):
    subprocess.run([go, "build", "-o", str(binaries / binary), "./collector/cmd/" + cmd], cwd=app, check=True)

def install(label, args, interval):
    domain = "gui/" + str(os.getuid())
    path = labels / (label + ".plist")
    config = dict(Label=label, ProgramArguments=args, RunAtLoad=True, StartInterval=interval, WorkingDirectory=str(project),
                  StandardOutPath=str(data / (label + ".log")), StandardErrorPath=str(data / (label + ".err")))
    encoded = plistlib.dumps(config)
    service = domain + "/" + label
    if subprocess.run(["launchctl", "print", service], capture_output=True).returncode == 0:
        if path.exists() and path.read_bytes() == encoded:
            return
        subprocess.run(["launchctl", "bootout", service], check=True)
    with path.open("wb") as f:
        f.write(encoded)
    subprocess.run(["launchctl", "bootstrap", domain, str(path)], check=True)

for source in ("softmagazin", "softmap"):
    install("local.aifence.market-catalog-"+source,
            [str(binaries/"fullcatalog"), "-source", source, "-out", str(data), "-refresh-after", "3h"], 10800)
install("local.aifence.market-catalog-export", [str(binaries/"catalogexport"), "-dir", str(data)], 60)

install("local.aifence.marketwatch.softline", [str(binaries/"softlinefull"), "-out", str(data/"softline")], 10800)
