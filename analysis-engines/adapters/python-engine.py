#!/usr/bin/env python3
"""Offline source-analysis bridge. No paths or commands are accepted from clients."""
import hashlib
import importlib.metadata
import ctypes.util
import json
import os
from pathlib import Path
import sys
import tempfile

LIMIT = 16 << 20
SLITHER_COMMIT = 'eef5df948b8992eff85786b6b0bb4a193933e2c9'
CRYTIC_COMMIT = '3d27afa70458b95c8660ba53b621ec6e72af08d4'

def version(package):
    return importlib.metadata.version(package)

def compile_vyper(payload):
    import vyper
    from vyper.cli.vyper_json import compile_json
    wanted = payload['compiler_version'].split('+')[0]
    actual = vyper.__version__
    if actual != wanted:
        raise ValueError('COMPILER_UNAVAILABLE:' + wanted + ':installed=' + actual)
    requested = payload['compiler_version']
    long_version = getattr(vyper, '__long_version__', actual)
    if '+' in requested and requested != long_version:
        raise ValueError('COMPILER_IDENTITY_MISMATCH:' + requested + ':installed=' + long_version)
    standard = payload['input']
    if standard.get('language') != 'Vyper':
        raise ValueError('SOURCE_LANGUAGE_UNSUPPORTED')
    standard = json.loads(json.dumps(standard))
    settings = standard.setdefault('settings', {})
    settings['outputSelection'] = {'*': ['abi', 'ast', 'layout', 'evm.deployedBytecode.object', 'evm.methodIdentifiers']}
    output = compile_json(standard)
    errors = [x for x in output.get('errors', []) if x.get('severity') == 'error']
    if errors:
        raise ValueError('COMPILATION_FAILED:' + ';'.join(x.get('message', '') for x in errors)[:500])
    return {'version': actual, 'compiler_identity': long_version, 'output': output}

def mapped_location(mapping):
    return {'file': str(mapping.filename.relative), 'start': mapping.start, 'length': mapping.length,
            'lines': list(mapping.lines), 'column_start': mapping.starting_column,
            'column_end': mapping.ending_column}

def slither_analysis(payload):
    from crytic_compile import CryticCompile
    from crytic_compile.platform.solc_standard_json import SolcStandardJson
    from slither.slither import Slither
    source = payload['source']
    if source['stdJsonInput']['language'] not in ['Solidity', 'Vyper']:
        raise ValueError('SLITHER_LANGUAGE_REQUIRES_ADAPTER')
    compiler = source['compilation']['compilerVersion']
    shim = str(Path(__file__).with_name('solc-shim.mjs'))
    directory = tempfile.TemporaryDirectory(prefix='contract-slither-')
    work = Path(directory.name).resolve()
    for name, item in source['stdJsonInput']['sources'].items():
        target = (work / name).resolve()
        if not target.is_relative_to(work) or Path(name).is_absolute():
            raise ValueError('SLITHER_SOURCE_PATH_UNSUPPORTED')
        if not isinstance(item.get('content'), str):
            raise ValueError('SLITHER_INLINE_SOURCE_REQUIRED')
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(item['content'], encoding='utf-8')
    os.chdir(work)
    if source['stdJsonInput']['language'] == 'Vyper':
        from crytic_compile.platform.vyper import VyperStandardJson
        platform = VyperStandardJson()
        platform.standard_json_input['sources'] = source['stdJsonInput']['sources']
        original_settings = source['stdJsonInput'].get('settings', {})
        platform.standard_json_input['settings'].update({k:v for k,v in original_settings.items() if k != 'outputSelection'})
        compilation = CryticCompile(platform, vyper=str(Path(sys.executable).with_name('vyper')))
    else:
        standard=json.loads(json.dumps(source['stdJsonInput']))
        standard.setdefault('settings',{})['outputSelection']={'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}
        compilation = CryticCompile(SolcStandardJson(standard), solc=shim,
                                   solc_env={**os.environ, 'CA_SOLC_VERSION': compiler}, solc_disable_warnings=True,
                                   solc_working_dir=str(work))
    analyzer = Slither(compilation)
    fq = source['compilation']['fullyQualifiedName']
    file_name, contract_name = fq.rsplit(':', 1)
    candidates = analyzer.get_contract_from_name(contract_name)
    candidates = [x for x in candidates if str(x.source_mapping.filename.relative).replace('\\', '/').endswith(file_name.replace('\\', '/'))]
    if len(candidates) != 1:
        raise ValueError('SLITHER_CONTRACT_IDENTITY_AMBIGUOUS')
    contract = candidates[0]
    functions = []
    total_operations = 0
    for function in contract.functions_and_modifiers:
        if not function.is_implemented:
            continue
        nodes = []
        for node in function.nodes:
            operations = []
            for index, ir in enumerate(node.irs):
                total_operations += 1
                if total_operations > 50000:
                    raise ValueError('SLITHER_OPERATION_BUDGET_EXCEEDED')
                text = str(ir)
                if len(text.encode()) > 8192:
                    raise ValueError('SLITHER_EXPRESSION_TOO_LARGE')
                operations.append({'index': index, 'op': type(ir).__name__, 'text': text,
                    'reads': [str(x) for x in ir.read], 'lvalue': str(ir.lvalue) if hasattr(ir, 'lvalue') and ir.lvalue is not None else None})
            nodes.append({'node_id': node.node_id, 'node_type': str(node.type),
                          'location': mapped_location(node.source_mapping), 'operations': operations,
                          'successors': [x.node_id for x in node.sons]})
        functions.append({'signature': function.solidity_signature, 'full_name': function.full_name,
                          'visibility': function.visibility, 'contract': function.contract_declarer.name,
                          'location': mapped_location(function.source_mapping),
                          'state_reads': [x.canonical_name for x in function.all_state_variables_read()],
                          'state_writes': [x.canonical_name for x in function.all_state_variables_written()],
                          'nodes': nodes})
    return {'engine': 'slither', 'version': version('slither-analyzer'), 'source_commit': SLITHER_COMMIT,
            'crytic_compile_version': version('crytic-compile'), 'crytic_compile_commit': CRYTIC_COMMIT,
            'compiler': compiler, 'functions': functions, 'operation_count': total_operations}

def main():
    raw = sys.stdin.buffer.read((24 << 20) + 1)
    if len(raw) > 24 << 20:
        raise ValueError('ENGINE_INPUT_TOO_LARGE')
    payload = json.loads(raw)
    operation = payload.get('operation')
    if operation == 'compile_vyper': result = compile_vyper(payload)
    elif operation == 'slither': result = slither_analysis(payload)
    elif operation == 'versions':
        result = {name: version(name) for name in ['slither-analyzer', 'crytic-compile', 'vyper']}
        result['z3_shared_library'] = ctypes.util.find_library('z3')
    else: raise ValueError('PYTHON_OPERATION_UNSUPPORTED')
    encoded = json.dumps(result, ensure_ascii=False, separators=(',', ':')).encode()
    if len(encoded) > LIMIT: raise ValueError('PYTHON_OUTPUT_BUDGET_EXCEEDED')
    sys.stdout.buffer.write(encoded)

if __name__ == '__main__':
    try: main()
    except Exception as error:
        sys.stderr.write(type(error).__name__ + ':' + str(error)[:800] + '\n')
        raise SystemExit(1)
