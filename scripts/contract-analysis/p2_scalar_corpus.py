"""Re-run only the previously exposed sealed material with a frozen scalar image."""
import pathlib
from p2_recovery_corpus import run
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-scalars'
assert BASE.is_symlink()
directory=BASE/'corpus';directory.mkdir(parents=True,exist_ok=True)
image=(BASE/'engine-image.txt').read_text().strip();run(image,'candidate-18','',out_base=directory)
