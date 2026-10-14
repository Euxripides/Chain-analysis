"""Compile the pinned decompiler serially during image construction."""
import os
from pathlib import Path
import sys

root = Path('/opt/gigahorse/gigahorse-toolchain')
os.chdir(root)
sys.path.insert(0, str(root))
import gigahorse
from src.runners import DecompilerFactGenerator, compile_datalog

args = gigahorse.parser.parse_args(['/tmp/warm.hex'])
args.context_depth = gigahorse.MAIN_DECOMPILER_MAX_CONTEXT_DEPTH
args.early_cloning = False
args.improved_ssa = False
args.disable_precise_fallback = False
args.disable_scalable_fallback = False
gigahorse.args = args
generator = DecompilerFactGenerator(args, '.*.hex')
files = list(dict.fromkeys(generator.get_datalog_files() + [gigahorse.DEFAULT_INLINER_DL]))
cache = root / 'cache'
cache.mkdir(exist_ok=True)
for spec in files:
    print('PRECOMPILE', spec, flush=True)
    compile_datalog(spec, '/usr/local/bin/souffle', str(cache), True, gigahorse.get_souffle_macros())
print('GIGAHORSE_COMPILED', len(files), flush=True)
