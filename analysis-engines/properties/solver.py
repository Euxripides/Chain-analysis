"""Bounded SMT checks using the Z3 shared library in the frozen base image."""
import ctypes as C
import ctypes.util
import json
import sys
import time


def solve(payload):
    lib = C.CDLL(ctypes.util.find_library('z3') or 'libz3.so.4')
    for name, restype, args in [
        ('Z3_mk_config', C.c_void_p, []), ('Z3_del_config', None, [C.c_void_p]),
        ('Z3_mk_context', C.c_void_p, [C.c_void_p]), ('Z3_del_context', None, [C.c_void_p]),
        ('Z3_eval_smtlib2_string', C.c_char_p, [C.c_void_p, C.c_char_p]),
        ('Z3_get_full_version', C.c_char_p, []),
    ]:
        fn = getattr(lib, name)
        fn.restype, fn.argtypes = restype, args
    handler_type = C.CFUNCTYPE(None, C.c_void_p, C.c_int)
    lib.Z3_set_error_handler.argtypes = [C.c_void_p, handler_type]
    results = []
    for query in payload.get('queries', [])[:24]:
        started = time.monotonic()
        config = lib.Z3_mk_config()
        ctx = lib.Z3_mk_context(config)
        lib.Z3_del_config(config)
        errors = []
        handler = handler_type(lambda _, code: errors.append(code))
        lib.Z3_set_error_handler(ctx, handler)
        try:
            def evaluate(s):
                if len(s.encode()) > 262144:
                    raise ValueError('SMT_QUERY_BUDGET')
                text = lib.Z3_eval_smtlib2_string(ctx, s.encode()).decode()
                if errors or '(error ' in text:
                    raise ValueError('SMT_QUERY_ERROR:' + text[:300])
                return text.strip()
            evaluate(query['declarations'])
            evaluate('(push)\n(assert ' + query['domain'] + ')')
            domain = evaluate('(check-sat)')
            if domain == 'sat':
                evaluate('(assert ' + query['violation'] + ')')
                decision = evaluate('(check-sat)')
                model = evaluate('(get-value (' + ' '.join(query['variables']) + '))') if decision == 'sat' and query['variables'] else None
            else:
                decision, model = 'not_checked', None
            results.append({'property_id': query['property_id'], 'domain': domain, 'decision': decision,
                            'counterexample_smt': model, 'version': lib.Z3_get_full_version().decode(),
                            'duration_ms': round((time.monotonic() - started) * 1000)})
        except Exception as error:
            results.append({'property_id': query['property_id'], 'decision': 'error', 'error': str(error)[:500]})
        finally:
            lib.Z3_del_context(ctx)
    return results


if __name__ == '__main__':
    payload = json.loads(sys.stdin.buffer.read(4 << 20))
    print(json.dumps(solve(payload)))
