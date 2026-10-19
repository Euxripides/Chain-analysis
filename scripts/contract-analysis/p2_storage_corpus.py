"""Re-evaluate only the existing exposed sealed materials with the storage engine."""
import argparse,pathlib
from p2_recovery_corpus import run
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-storage'
target=pathlib.Path('/data/backups/codex-contract-general-20261017/storage-reference-corpus');target.mkdir(parents=True,exist_ok=True)
directory=BASE/'corpus'
if not directory.exists():directory.symlink_to(target,target_is_directory=True)
assert directory.is_symlink()and directory.resolve()==target.resolve()
p=argparse.ArgumentParser();p.add_argument('--context-match',default='');a=p.parse_args()
image=(BASE/'engine-image.txt').read_text().strip();run(image,'candidate-17',a.context_match,out_base=directory)
