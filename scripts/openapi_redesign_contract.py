"""Typed contracts for the approved blue explorer and dataset export changes."""
import copy


def enrich(doc):
    schemas = doc['components'].setdefault('schemas', {})
    string = {'type': 'string'}
    integer = {'type': 'integer', 'minimum': 0}
    stamp = {'type': ['string', 'null'], 'format': 'date-time'}
    raw = {'type': 'string', 'pattern': r'^-?\d+$', 'description': 'Exact integer; never converted through floating point.'}
    def ref(name): return {'$ref': '#/components/schemas/' + name}
    def array(item): return {'type': 'array', 'items': item}
    def obj(props, required=()):
        value = {'type': 'object', 'properties': props}
        if required: value['required'] = list(required)
        return value
    def envelope(data):
        return obj({'success': {'const': True}, 'data': data, 'meta': {'$ref': './common/envelope.yaml#/Meta'}}, ['success', 'data', 'meta'])
    def parameter(name, schema=string, where='query', required=False):
        return {'name': name, 'in': where, 'required': required, 'schema': schema}
    def success(operation, data, status='200'):
        operation['responses'][status] = {'description': 'Success', 'content': {'application/json': {'schema': envelope(data)}}}
        operation['x-contract-level'] = 'typed'
    def error(operation, status, description):
        operation['responses'][str(status)] = {'description': description, 'content': {'application/json': {'schema': {'$ref': './common/errors.yaml#/ErrorEnvelope'}}}}

    schemas['ExplorerFilter'] = obj({
        'chain': string, 'addresses': {'type': 'array', 'maxItems': 100, 'items': string},
        'datasets': array(string), 'token': {'type': 'string', 'description': 'Contract(s), or a case-insensitive partial symbol; chain and contract remain asset identity.'},
        'direction': {'type': 'string', 'enum': ['', 'in', 'out', 'self', 'internal']},
        'counterparty': string, 'from_address': string, 'to_address': string, 'hide_zero': {'type': 'boolean'},
        'from_time': stamp, 'to_time': stamp, 'as_of': stamp,
        'min_amount': {'type': 'string', 'pattern': r'^\d+(\.\d+)?$'},
        'max_amount': {'type': 'string', 'pattern': r'^\d+(\.\d+)?$'},
        'block_from': integer, 'block_to': integer,
        'interval': {'type': 'string', 'enum': ['day', 'hour']},
        'timezone': {'type': 'string', 'description': 'IANA timezone, default Asia/Shanghai.'},
    })
    total = {name: raw for name in ['in_raw', 'out_raw', 'net_raw', 'internal_raw', 'self_raw']}
    total.update({name: integer for name in ['token_decimals', 'event_count', 'transaction_count', 'missing_amount_rows', 'failed_event_count']})
    total.update({name: string for name in ['dataset', 'token_address', 'token_symbol']})
    total.update(first_seen=stamp, last_seen=stamp)
    schemas['AssetFlowTotal'] = obj(total, total)
    schemas['StatisticsGroup'] = obj({'key': string, 'total': ref('AssetFlowTotal'), 'counterparty': string, 'address': string, 'bucket': string, 'common_addresses': array(string), 'share_percent': string}, ['key', 'total'])
    event = obj({'dataset': string, 'tx_hash': string, 'event_index': integer, 'token_address': string, 'token_symbol': string, 'token_decimals': integer, 'timestamp': stamp, 'from_address': string, 'to_address': string, 'amount_raw': string, 'source_ingested_at': stamp, 'status': integer, 'block_number': integer})
    properties = {'filter': ref('ExplorerFilter'), 'data_version': string, 'transaction_count': integer, 'event_count': integer, 'counterparty_count': integer, 'first_seen': stamp, 'last_seen': stamp, 'top_transfers': array(event)}
    properties.update({name: array(ref('StatisticsGroup')) for name in ['assets', 'trend', 'hours', 'distribution', 'counterparties', 'addresses']})
    schemas['ExplorerStatistics'] = obj(properties, properties)
    schemas['DatasetFile'] = obj({'dataset': string, 'name': string, 'rows': integer, 'bytes': integer, 'sha256': {'type': 'string', 'pattern': '^[0-9a-f]{64}$'}, 'download_available': {'type': 'boolean'}}, ['dataset', 'name', 'rows', 'bytes', 'sha256', 'download_available'])
    schemas['ExportResult'] = obj({'id': {'type': 'string', 'format': 'uuid'}, 'job_id': {'type': ['string', 'null']}, 'dataset': string, 'format': {'type': 'string', 'enum': ['csv', 'xlsx', 'jsonl', 'parquet']}, 'zip': {'type': 'boolean'}, 'status': string, 'rows': integer, 'bytes': {'type': ['integer', 'null']}, 'sha256': string, 'created_at': stamp, 'finished_at': stamp, 'expires_at': stamp, 'download_available': {'type': 'boolean'}, 'files': array(ref('DatasetFile'))})
    schemas['ExportRequest'] = obj({'job_id': {'type': 'string', 'format': 'uuid'}, 'chain': string, 'addresses': {'type': 'array', 'minItems': 1, 'maxItems': 100, 'items': string}, 'datasets': array(string), 'format': {'type': 'string', 'enum': ['csv', 'xlsx', 'jsonl', 'parquet']}, 'content': {'type': 'string', 'enum': ['details', 'statistics', 'counterparties']}, 'file_layout': {'type': 'string', 'enum': ['dataset'], 'description': 'Exactly one file per dataset; multiple datasets form a ZIP.'}, 'filters': ref('ExplorerFilter'), 'start_time': stamp, 'end_time': stamp, 'include_meta': {'type': 'boolean'}, 'include_decoded_input': {'type': 'boolean'}, 'force_zip': {'type': 'boolean'}}, ['format'])
    paths = doc['paths']
    stats = paths['/api/v2/explorer/addresses/{chain}/{address}/statistics']['get']
    stats['description'] = 'Read-only local index aggregation, canonical event identity and exact raw amounts. All chosen events are aggregated on the server. Group internal/self transfers remain separate. Coverage accompanies results. Budget refusal returns no partial success.'
    stats['parameters'] = [parameter('chain', where='path', required=True), parameter('address', where='path', required=True)]
    for name, schema in schemas['ExplorerFilter']['properties'].items():
        if name in ['chain', 'addresses', 'datasets', 'block_from', 'block_to']: continue
        stats['parameters'].append(parameter(name, {'type': 'string', 'enum': ['1', '0']} if name == 'hide_zero' else schema))
    stats['parameters'] += [parameter('addresses', {'type': 'string', 'description': 'Comma separated address group, at most 100.'}), parameter('datasets', {'type': 'string', 'description': 'Comma separated transactions, token_transfers, internal_transactions.'})]
    success(stats, obj({'statistics': ref('ExplorerStatistics'), 'coverage': array({'type': 'object'}), 'source': {'type': 'object'}, 'calculation': {'type': 'object'}}))
    error(stats, 422, 'QUERY_SCAN_BUDGET: shorten range or specify contract; no partial aggregates.')
    error(stats, 503, 'DATA_UNAVAILABLE: local index or scan temporarily unavailable.')
    for path in ['/api/v2/explorer/blocks/{chain}/{block}', '/api/v2/explorer/tokens/{chain}/{contract}', '/api/v2/explorer/tokens/{chain}/{contract}/transfers', '/api/v2/explorer/tokens/{chain}/{contract}/logo', '/api/v2/explorer/tokens']:
        operation = paths[path]['get']
        operation['parameters'] = [parameter(name, where='path', required=True) for name in ['chain', 'block', 'contract'] if '{'+name+'}' in path]
        error(operation, 503, 'Local data unavailable; missing coverage is never represented as a zero balance/supply.')
        if path.endswith('/logo'):
            operation['responses']['200'] = {'description': 'Verified chain + contract logo', 'content': {'image/png': {'schema': {'type': 'string', 'format': 'binary'}}}}
            error(operation, 404, 'No logo resource for this exact contract.')
        else:
            if path.endswith('/transfers') or '/blocks/' in path:
                operation['parameters'] += [parameter('limit', {'type': 'integer', 'minimum': 1, 'maximum': 200}), parameter('cursor')]
                success(operation, obj({'items': array({'type': 'object'}), 'summary': {'type': 'object'}, 'source': string}))
            elif path == '/api/v2/explorer/tokens':
                operation['parameters'] += [parameter('chain', required=True), parameter('q'), parameter('limit', {'type': 'integer', 'minimum': 1, 'maximum': 50})]
                success(operation, obj({'items': array({'type': 'object'})}))
            else: success(operation, obj({'chain': string, 'contract_address': string, 'metadata': {'type': ['object', 'null']}, 'source': string}))
    create = paths['/api/v2/exports']['post']
    create['parameters'] = [parameter('X-CSRF-Token', where='header', required=True), parameter('Idempotency-Key', where='header', required=True)]
    create['requestBody'] = {'required': True, 'content': {'application/json': {'schema': ref('ExportRequest')}}}
    success(create, {'type': 'object'}, '201')
    create['description'] = 'Asynchronous generation with a stored normalized filter snapshot. Legacy requests preserve prefetch and bounded job exports. Dataset layout exposes real per-file rows, bytes and SHA256. Aggregates use local canonical source data.'
    listing = paths['/api/v2/exports']['get']
    listing['parameters'] = [parameter(name) for name in ['q', 'status', 'format', 'cursor']] + [parameter('limit', {'type': 'integer', 'minimum': 1, 'maximum': 100})]
    success(listing, obj({'items': array(ref('ExportResult')), 'scope': {'type': 'object'}}))
    detail = paths['/api/v2/exports/{id}']['get']
    detail['parameters'] = [parameter('id', {'type': 'string', 'format': 'uuid'}, 'path', True)]
    success(detail, ref('ExportResult'))
    ticket = paths['/api/v2/exports/{id}/files/{dataset}/download-ticket']['post']
    ticket['parameters'] = [parameter('id', {'type': 'string', 'format': 'uuid'}, 'path', True), parameter('dataset', where='path', required=True), parameter('X-CSRF-Token', where='header', required=True)]
    success(ticket, obj({'ticket_id': {'type': 'string', 'format': 'uuid'}, 'url': string, 'expires_in_seconds': {'const': 60}, 'single_use': {'const': True}}))
    ticket['description'] = 'Bound to identity, organization, workspace and this dataset. Created on user action only. GET consumes; HEAD inspects without consuming. Same identity may continue with Range until export expiry (maximum 24 hours).'
    for status, description in [(404, 'Export/dataset outside caller scope.'), (409, 'File not available.'), (503, 'Object missing.')]: error(ticket, status, description)
    doc['x-typed-scope'] = 'Blue explorer statistics/object queries and dataset export operations; other existing operations remain index-only.'
    return doc
