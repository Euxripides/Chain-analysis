"""Measure the existing sealed material offline without creating a new blind set."""
import argparse,pathlib
from p2_recovery_corpus import run
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-memory';target=pathlib.Path('/data/backups/codex-contract-general-20261017/memory-abi-corpus');directory=OUT/'corpus'
target.mkdir(parents=True,exist_ok=True)
if not directory.exists():directory.symlink_to(target,target_is_directory=True)
assert directory.is_symlink()and directory.resolve()==target.resolve()
p=argparse.ArgumentParser();p.add_argument('--context-match',default='');args=p.parse_args();image=(OUT/'engine-image.txt').read_text().strip()
run(image,'candidate-16',args.context_match,out_base=directory)
