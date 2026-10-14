"""Build only the isolated coverage candidate; logs and locks stay in that candidate."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]
WORK = ROOT / '.work/coverage'
OUT = ROOT / 'acceptance/contract-analysis-coverage'
VENDORS = WORK / 'vendors'

def run(command, name):
    OUT.mkdir(parents=True, exist_ok=True)
    with (OUT / (name + '.log')).open('w') as log:
        result = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT)
    print(name, 'EXIT', result.returncode, flush=True)
    print((OUT / (name + '.log')).read_text()[-2200:], flush=True)
    if result.returncode:
        raise SystemExit(result.returncode)

def engine():
    build = WORK / 'build'
    build.mkdir(parents=True, exist_ok=True)
    source = ROOT / 'analysis-engines'
    for p in source.glob('*.mjs'):
        shutil.copy2(p, build / p.name)
    for name in ['adapters', 'rules', 'ir']:
        shutil.copytree(source / name, build / name, dirs_exist_ok=True)
    shutil.copy2(source / 'Dockerfile.coverage', build / 'Dockerfile')
    shutil.copytree(source / 'notices',build / 'notices',dirs_exist_ok=True)
    (build / 'bin').mkdir(exist_ok=True)
    for source_name, target in [('gigahorse-toolchain-1b06cef82f0a', 'gigahorse'),
                               ('crytic-compile-3d27afa70458', 'crytic-compile'),
                               ('slither-eef5df948b89', 'slither')]:
        shutil.copytree(VENDORS / source_name, build / 'vendor' / target, dirs_exist_ok=True,
                        ignore=shutil.ignore_patterns('.git', '.venv', '__pycache__', '.pytest_cache'))
    freeze = (source / 'requirements.coverage.lock').read_text().splitlines()
    (build / 'requirements.lock').write_text('\n'.join(x for x in freeze if ' @ file:' not in x) + '\n')
    run(['docker', 'build', '--progress=plain', '-t', 'platform-contract-coverage:20261014', str(build)], 'engine-build')
    digest = subprocess.check_output(['docker', 'image', 'inspect', 'platform-contract-coverage:20261014', '--format', '{{.Id}}']).decode().strip()
    (OUT / 'engine-image.txt').write_text(digest + '\n')
    print('ENGINE_DIGEST', digest, flush=True)

def sbf():
    vendor = VENDORS / 'sbpf-c2b66f1080ee'
    sha = subprocess.check_output(['git', '-C', str(vendor), 'rev-parse', 'HEAD']).decode().strip()
    assert sha == 'c2b66f1080eeec68a17747740d2d0ebee208a0a8'
    subprocess.run(['git', '-C', str(vendor), 'diff', '--quiet'], check=True)
    source = ROOT / 'analysis-engines/sbf'
    build = WORK / 'sbf-build'
    build.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source / 'src', build / 'src', dirs_exist_ok=True)
    manifest = (source / 'Cargo.toml').read_text()
    manifest = manifest.replace('{ git = "https://github.com/anza-xyz/sbpf", rev = "c2b66f1080eeec68a17747740d2d0ebee208a0a8", default-features = false }',
                                '{ path = "/vendor/sbpf", default-features = false }')
    (build / 'Cargo.toml').write_text(manifest)
    cache = WORK / 'cargo'
    cache.mkdir(parents=True, exist_ok=True)
    image = 'docker.1ms.run/library/rust@sha256:2775a09d208ff0d7c1f50490c45b62db929e87ba1dcbc3f2132ac71a704bcdd3'
    run(['docker', 'run', '--rm', '--name', 'contract-coverage-sbf-build', '--cpus', '2', '--memory', '2g', '--memory-swap', '2g', '--pids-limit', '128',
         '--mount', 'type=bind,src=' + str(build) + ',dst=/work',
         '--mount', 'type=bind,src=' + str(vendor) + ',dst=/vendor/sbpf,readonly',
         '--mount', 'type=bind,src=' + str(cache) + ',dst=/cargo', '-w', '/work', '-e', 'CARGO_HOME=/cargo', '-e', 'CARGO_BUILD_JOBS=2',
         image, 'cargo', 'build', '--release'], 'sbf-build')
    binary = ROOT / 'analysis-engines/bin/contract-sbf-analysis'
    binary.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(build / 'target/release/contract-sbf-analysis', binary)
    (OUT / 'sbf-build-identity.json').write_text(json.dumps({'vendor_commit': sha, 'rust_image': image,
        'dependency_source': 'verified_local_checkout', 'cargo_lock': str(build / 'Cargo.lock')}, indent=2))

def svm():
    vendor = VENDORS / 'mollusk-bd75dd7937a8'
    sha = subprocess.check_output(['git','-C',str(vendor),'rev-parse','HEAD']).decode().strip()
    assert sha == 'bd75dd7937a8e51fa3b655a39b76a9c6170f9d2c'
    subprocess.run(['git','-C',str(vendor),'diff','--quiet'],check=True)
    source = ROOT / 'analysis-engines/svm'
    build = WORK / 'svm-build'; build.mkdir(parents=True,exist_ok=True)
    shutil.copytree(source / 'src',build / 'src',dirs_exist_ok=True)
    manifest = (source / 'Cargo.toml').read_text().replace('{ git = "https://github.com/anza-xyz/mollusk", rev = "bd75dd7937a8e51fa3b655a39b76a9c6170f9d2c", features = ["inner-instructions"] }','{ path = "/vendor/mollusk/harness", features = ["inner-instructions"] }')
    (build / 'Cargo.toml').write_text(manifest)
    cache=WORK / 'cargo';cache.mkdir(parents=True,exist_ok=True)
    if not (build / 'Cargo.lock').exists(): shutil.copy2(vendor / 'Cargo.lock', build / 'Cargo.lock')
    image='docker.1ms.run/library/rust@sha256:2775a09d208ff0d7c1f50490c45b62db929e87ba1dcbc3f2132ac71a704bcdd3'
    run(['docker','run','--rm','--name','contract-coverage-svm-build','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128',
         '--mount','type=bind,src='+str(build)+',dst=/work','--mount','type=bind,src='+str(vendor)+',dst=/vendor/mollusk,readonly',
         '--mount','type=bind,src='+str(cache)+',dst=/cargo','-w','/work','-e','CARGO_HOME=/cargo','-e','CARGO_BUILD_JOBS=2',image,
         'cargo','build','--release'],'svm-build')
    binary=ROOT / 'analysis-engines/bin/contract-svm-simulate';binary.parent.mkdir(parents=True,exist_ok=True)
    shutil.copy2(build / 'target/release/contract-svm-simulate',binary)
    (OUT / 'svm-build-identity.json').write_text(json.dumps({'vendor_commit':sha,'rust_image':image,'dependency_source':'verified_local_checkout'},indent=2))

def tvm():
    vendor=VENDORS / 'java-tron-b33eed89a6a4'
    sha=subprocess.check_output(['git','-C',str(vendor),'rev-parse','HEAD']).decode().strip()
    assert sha=='b33eed89a6a424c498d4fb1b03ca2c86eddf4840'
    subprocess.run(['git','-C',str(vendor),'diff','--quiet'],check=True)
    build=WORK / 'tvm-build';build.mkdir(parents=True,exist_ok=True)
    shutil.copytree(vendor,build,dirs_exist_ok=True,ignore=shutil.ignore_patterns('.git','.gradle','build'))
    (build/'coverage.init.gradle').write_text('allprojects { tasks.withType(Test).configureEach { maxHeapSize="512m"; maxParallelForks=1 } }\n')
    image='docker.1ms.run/library/eclipse-temurin@sha256:b4a2bba830372dc73efb8bf772cdfd8fb43c7a9035d101e64cd393123f691009'
    run(['docker','run','--rm','--name','contract-coverage-tvm-build','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','256',
         '--mount','type=bind,src='+str(build)+',dst=/work','-w','/work','-e','GRADLE_USER_HOME=/work/.gradle',image,
         'bash','gradlew','--no-daemon','--no-parallel','--max-workers=2','-Dorg.gradle.jvmargs=-Xms128m -Xmx768m','-I','coverage.init.gradle',
         ':actuator:dependencies','--configuration','runtimeClasspath'],'tvm-dependencies')
    (OUT/'tvm-build-identity.json').write_text(json.dumps({'vendor_commit':sha,'jdk_image':image,'stage':'dependency_resolution'},indent=2))

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('target', choices=['engine', 'sbf', 'svm', 'tvm'])
    args = parser.parse_args()
    if ROOT.name != 'contract-coverage-20261014':
        raise SystemExit('Isolated coverage candidate required')
    globals()[args.target]()
