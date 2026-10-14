use std::io::{self, Read};
use std::sync::Arc;
use base64::{engine::general_purpose::STANDARD, Engine};
use serde_json::{json, Value};
use solana_sbpf::{elf::Executable, memory_region::MemoryMapping, program::BuiltinProgram, static_analysis::Analysis, vm::{Config, ContextObject}};

struct StaticContext;
impl ContextObject for StaticContext {
    fn consume(&mut self, _amount: u64) { panic!("static analysis cannot execute instructions"); }
    fn get_remaining(&self) -> u64 { 0 }
    fn active_mapping_ptr(&mut self) -> std::ptr::NonNull<MemoryMapping> { panic!("static analysis has no execution memory"); }
}

fn analyze(input: Value) -> Result<Value, String> {
    let encoded = input.get("elf_base64").and_then(Value::as_str).ok_or("ELF_BYTES_REQUIRED")?;
    let bytes = STANDARD.decode(encoded).map_err(|_| "ELF_BASE64_INVALID")?;
    if bytes.len() > 16 << 20 { return Err("ELF_INPUT_TOO_LARGE".into()); }
    if !bytes.starts_with(b"\x7fELF") { return Err("ELF_HEADER_INVALID".into()); }
    let loader = Arc::new(BuiltinProgram::<StaticContext>::new_loader(Config { enable_symbol_and_section_labels: true, ..Config::default() }));
    let executable = Executable::<StaticContext>::from_elf(&bytes, loader).map_err(|e| format!("SBF_ELF_LOAD_FAILED:{e}"))?;
    let analysis = Analysis::from_executable(&executable).map_err(|e| format!("SBF_ANALYSIS_FAILED:{e}"))?;
    if analysis.instructions.len() > 100_000 { return Err("SBF_INSTRUCTION_BUDGET_EXCEEDED".into()); }
    let mut disassembly = Vec::new();
    analysis.disassemble(&mut disassembly).map_err(|e| e.to_string())?;
    let instructions = analysis.instructions.iter().enumerate().map(|(index, insn)| {
        json!({"pc": insn.ptr, "opcode": insn.opc, "dst": insn.dst, "src": insn.src, "offset": insn.off,
            "immediate": insn.imm.to_string(), "text": analysis.disassemble_instruction(insn, index)})
    }).collect::<Vec<_>>();
    let functions = analysis.functions.iter().map(|(pc, (key, name))| json!({"pc": pc, "symbol_hash": key, "name": name})).collect::<Vec<_>>();
    let control_flow = analysis.cfg_nodes.iter().map(|(pc, node)| json!({"pc": pc, "label": node.label,
        "predecessors": node.sources, "successors": node.destinations})).collect::<Vec<_>>();
    Ok(json!({"engine": "solana-sbpf", "version": "0.25.0", "source_commit": "c2b66f1080eeec68a17747740d2d0ebee208a0a8",
        "mode": "static_analysis", "sbpf_version": format!("{:?}", executable.get_sbpf_version()), "entrypoint": analysis.entrypoint,
        "instructions": instructions, "functions": functions, "control_flow": control_flow, "disassembly": String::from_utf8_lossy(&disassembly),
        "function_names_basis": "elf_symbols_or_generated_labels", "historical_execution_proved": false}))
}

fn main() {
    let mut bytes = Vec::new();
    if io::stdin().take((24 << 20) + 1).read_to_end(&mut bytes).is_err() || bytes.len() > 24 << 20 {
        eprintln!("SBF_INPUT_TOO_LARGE"); std::process::exit(1);
    }
    let result = serde_json::from_slice(&bytes).map_err(|e| e.to_string()).and_then(analyze);
    match result {
        Ok(value) => println!("{}", value),
        Err(error) => { eprintln!("{}", error); std::process::exit(1); }
    }
}
