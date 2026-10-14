use std::{io::{self, Read}, str::FromStr};
use base64::{engine::general_purpose::STANDARD, Engine};
use mollusk_svm::Mollusk;
use serde_json::{json, Value};
use sha2::{Sha256, Digest};
use solana_account::Account;
use solana_instruction::{Instruction, AccountMeta};
use solana_pubkey::Pubkey;

fn text<'a>(v: &'a Value, key: &str) -> Result<&'a str, String> { v.get(key).and_then(Value::as_str).ok_or_else(|| format!("FIELD_REQUIRED:{key}")) }
fn key(v: &Value, name: &str) -> Result<Pubkey,String> { Pubkey::from_str(text(v,name)?).map_err(|_|format!("PUBKEY_INVALID:{name}")) }
fn integer(v: &Value, name: &str) -> Result<u64,String> { text(v,name)?.parse::<u64>().map_err(|_|format!("UINT64_INVALID:{name}")) }
fn bytes(v: &Value, name: &str) -> Result<Vec<u8>,String> { STANDARD.decode(text(v,name)?).map_err(|_|format!("BASE64_INVALID:{name}")) }
fn simulate(input: Value) -> Result<Value,String> {
    if input.get("mode").and_then(Value::as_str)!=Some("local_instruction_simulation") {return Err("LOCAL_SIMULATION_MODE_REQUIRED".into())}
    let mut svm=Mollusk::default();
    svm.compute_budget.compute_unit_limit=500_000;
    let mut programs=Vec::new();
    for program in input.get("programs").and_then(Value::as_array).ok_or("PROGRAMS_REQUIRED")? {
        let id=key(program,"program_id")?;let loader=key(program,"loader")?;let elf=bytes(program,"elf_base64")?;
        if elf.len()>4<<20{return Err("SIMULATION_PROGRAM_TOO_LARGE".into())}
        svm.add_program_with_loader_and_elf(&id,&loader,&elf);
        programs.push(json!({"program_id":id.to_string(),"elf_sha256":format!("{:x}",Sha256::digest(&elf)),"loader":loader.to_string()}));
    }
    let mut accounts=Vec::new();
    for a in input.get("accounts").and_then(Value::as_array).ok_or("ACCOUNTS_REQUIRED")? {
        if accounts.len()>=128{return Err("SIMULATION_ACCOUNT_BUDGET".into())}
        let address=key(a,"address")?;let data=bytes(a,"data_base64")?;if data.len()>1<<20{return Err("SIMULATION_ACCOUNT_TOO_LARGE".into())}
        accounts.push((address,Account{lamports:integer(a,"lamports_raw")?,data,owner:key(a,"owner")?,executable:a.get("executable").and_then(Value::as_bool).unwrap_or(false),rent_epoch:integer(a,"rent_epoch_raw")?}));
    }
    let mut instructions=Vec::new();
    for row in input.get("instructions").and_then(Value::as_array).ok_or("INSTRUCTIONS_REQUIRED")? {
        if instructions.len()>=32{return Err("SIMULATION_INSTRUCTION_BUDGET".into())}
        let data=bytes(row,"data_base64")?;if data.len()>4096{return Err("SIMULATION_INSTRUCTION_TOO_LARGE".into())}
        let metas=row.get("accounts").and_then(Value::as_array).ok_or("INSTRUCTION_ACCOUNTS_REQUIRED")?.iter().map(|m|{
            Ok(AccountMeta{pubkey:key(m,"address")?,is_signer:m.get("signer").and_then(Value::as_bool).ok_or("SIGNER_FLAG_REQUIRED")?,is_writable:m.get("writable").and_then(Value::as_bool).ok_or("WRITABLE_FLAG_REQUIRED")?})
        }).collect::<Result<Vec<_>,String>>()?;
        instructions.push(Instruction{program_id:key(row,"program_id")?,accounts:metas,data});
    }
    if instructions.is_empty(){return Err("SIMULATION_INSTRUCTIONS_EMPTY".into())}
    let result=svm.process_instruction_chain(&instructions,&accounts);
    let account_rows=result.resulting_accounts.iter().map(|(address,a)|{
        let before=accounts.iter().find(|(key,_)|key==address).map(|(_,a)|a);
        json!({"address":address.to_string(),"before_lamports_raw":before.map(|a|a.lamports.to_string()),"after_lamports_raw":a.lamports.to_string(),
            "delta_lamports_raw":before.map(|b|(i128::from(a.lamports)-i128::from(b.lamports)).to_string()),"owner":a.owner.to_string(),
            "before_data_sha256":before.map(|a|format!("{:x}",Sha256::digest(&a.data))),"after_data_sha256":format!("{:x}",Sha256::digest(&a.data)),"after_data_base64":STANDARD.encode(&a.data)})
    }).collect::<Vec<_>>();
    Ok(json!({"engine":"mollusk","version":"0.16.2","source_commit":"bd75dd7937a8e51fa3b655a39b76a9c6170f9d2c","mode":"local_instruction_simulation",
        "success":result.raw_result.is_ok(),"program_result":format!("{:?}",result.program_result),"raw_result":format!("{:?}",result.raw_result),
        "compute_units_consumed":result.compute_units_consumed.to_string(),"execution_time":result.execution_time.to_string(),"return_data_base64":STANDARD.encode(&result.return_data),
        "programs":programs,"accounts":account_rows,"inner_instructions":format!("{:?}",result.inner_instructions),
        "environment":{"feature_set":"mollusk_pinned_all_enabled_defaults","sysvars":"pinned_mollusk_defaults","compute_unit_limit":500000,"transaction_fee_included":false,"transaction_atomicity":"independent_instruction_sequence"},
        "historical_transaction_replay_verified":false}))
}
fn main() {
    let mut input=Vec::new();if io::stdin().take((24<<20)+1).read_to_end(&mut input).is_err()||input.len()>24<<20{eprintln!("SVM_INPUT_TOO_LARGE");std::process::exit(1)}
    let output=serde_json::from_slice(&input).map_err(|e|e.to_string()).and_then(simulate);
    match output{Ok(v)=>println!("{v}"),Err(e)=>{eprintln!("{e}");std::process::exit(1)}}
}
