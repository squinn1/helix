import logging
import pprint
import signal
import time
import traceback
import uuid
from io import StringIO
from typing import List, Optional

import torch
import transformers
from accelerate.logging import get_logger
from axolotl.cli import (
    check_accelerate_default_config,
    check_user_token,
    load_cfg,
    load_datasets,
)
from axolotl.common.cli import TrainerCliArgs, load_model_and_tokenizer
from axolotl.train import train
from axolotl.utils.chat_templates import chat_templates
from axolotl.utils.config import normalize_config
from fastapi import BackgroundTasks, FastAPI, HTTPException
from pydantic import BaseModel
from transformers import GenerationConfig, TextStreamer


class LevelFilter(logging.Filter):
    def __init__(self, levels):
        self.levels = levels

    def filter(self, record):
        return record.levelno in self.levels


log_stream = StringIO()
AXO_LOGGER = get_logger("axolotl.train")
print("PHIL", type(AXO_LOGGER))
AXO_LOGGER.addHandler(logging.StreamHandler(log_stream))

# Create FastAPI app
app = FastAPI()


class Hyperparameters(BaseModel):
    n_epochs: Optional[int] = 4
    batch_size: Optional[int] = 1
    learning_rate_multiplier: Optional[float] = 1.0


class CreateFineTuningJobRequest(BaseModel):
    training_file: str
    model: str
    validation_file: Optional[str] = None
    hyperparameters: Optional[Hyperparameters] = Hyperparameters()


class FineTuningJob(BaseModel):
    id: str
    model: str
    training_file: str
    validation_file: Optional[str]
    status: str
    created_at: int
    fine_tuned_model: Optional[str] = None
    hyperparameters: Hyperparameters
    trained_tokens: Optional[int] = 0
    result_files: Optional[List[str]] = []
    integrations: Optional[List[dict]] = []
    seed: Optional[int] = 0
    estimated_finish: Optional[int] = 0


class FineTuningEvent(BaseModel):
    id: str
    created_at: int
    level: str
    message: str
    data: Optional[dict] = None


class FineTuningEventList(BaseModel):
    object: str
    data: list[FineTuningEvent]
    has_more: bool


# In-memory storage to mock database
fine_tuning_jobs = {}
fine_tuning_events = {}


# Helper function to generate job ID
def generate_job_id():
    return f"ftjob-{uuid.uuid4()}"


# Function to run fine-tuning using Axolotl
def run_fine_tuning(
    job_id: str, model: str, training_file: str, hyperparameters: Hyperparameters
):
    orig_signal = signal.signal
    # Override signal, required so axolotl doesn't try and use signals in a thread
    signal.signal = lambda sig, handler: True

    try:
        # Update job status to running
        fine_tuning_jobs[job_id].status = "running"
        add_fine_tuning_event(job_id, "info", "Fine-tuning job started.")

        parsed_cfg = load_cfg("helix-mistral-instruct-v1.yml")

        parsed_cfg["datasets"][0]["path"] = training_file
        parsed_cfg["datasets"][0]["type"] = "chat_template"
        parsed_cfg["datasets"][0]["field_messages"] = "conversations"
        parsed_cfg["datasets"][0]["message_field_role"] = "from"
        parsed_cfg["datasets"][0]["message_field_content"] = "value"
        parsed_cfg["datasets"][0]["chat_template"] = "mistral_v1"
        parsed_cfg["datasets"][0]["roles"] = {}
        parsed_cfg["datasets"][0]["roles"]["user"] = ["human"]
        parsed_cfg["datasets"][0]["roles"]["assistant"] = ["gpt"]
        parsed_cfg["datasets"][0]["roles"]["system"] = ["system"]

        parsed_cfg["output_dir"] = f"/tmp/helix/results/{job_id}"

        # # Use Alpaca dataset for testing
        # parsed_cfg["datasets"][0]["path"] = "mhenrichsen/alpaca_2k_test"
        # parsed_cfg["datasets"][0]["type"] = "alpaca"

        pprint.pprint(parsed_cfg)

        check_accelerate_default_config()
        check_user_token()
        normalize_config(parsed_cfg)
        cli_args = TrainerCliArgs()
        dataset_meta = load_datasets(cfg=parsed_cfg, cli_args=cli_args)

        train(cfg=parsed_cfg, cli_args=cli_args, dataset_meta=dataset_meta)

        # Update job status to succeeded
        fine_tuning_jobs[job_id].status = "succeeded"
        fine_tuning_jobs[job_id].fine_tuned_model = f"{model}-fine-tuned"
        fine_tuning_jobs[job_id].result_files = [f"/tmp/helix/results/{job_id}"]
        add_fine_tuning_event(job_id, "info", "Fine-tuning job completed successfully.")

    except Exception as e:
        print(f"PHIL: {log_stream.getvalue()}")
        # Handle any errors that occur during the fine-tuning process
        fine_tuning_jobs[job_id].status = "failed"
        add_fine_tuning_event(
            job_id,
            "error",
            f"Fine-tuning job failed: {str(e)}. {log_stream.getvalue()}",
        )
        print(traceback.format_exc())

    signal.signal = orig_signal


# Mock function to add events (this would be called when real fine-tuning happens)
def add_fine_tuning_event(job_id: str, level: str, message: str):
    event = FineTuningEvent(
        id=str(uuid.uuid4()), created_at=int(time.time()), level=level, message=message
    )
    if job_id not in fine_tuning_events:
        fine_tuning_events[job_id] = []
    fine_tuning_events[job_id].append(event)


# 1. Create Fine-tuning Job
@app.post("/v1/fine_tuning/jobs", response_model=FineTuningJob)
async def create_fine_tuning_job(
    request: CreateFineTuningJobRequest, background_tasks: BackgroundTasks
):
    job_id = generate_job_id()
    job = FineTuningJob(
        id=job_id,
        model=request.model,
        training_file=request.training_file,
        validation_file=request.validation_file,
        status="queued",
        created_at=int(time.time()),
        hyperparameters=request.hyperparameters,
    )

    fine_tuning_jobs[job_id] = job

    # Add initial event
    add_fine_tuning_event(job_id, "info", "Fine-tuning job created and queued.")

    # Run fine-tuning in background
    background_tasks.add_task(
        run_fine_tuning,
        job_id,
        request.model,
        request.training_file,
        request.hyperparameters,
    )

    return job


# 2. List Fine-tuning Jobs
@app.get("/v1/fine_tuning/jobs", response_model=List[FineTuningJob])
async def list_fine_tuning_jobs(limit: Optional[int] = 20):
    return list(fine_tuning_jobs.values())[:limit]


# 3. Retrieve Fine-tuning Job
@app.get("/v1/fine_tuning/jobs/{fine_tuning_job_id}", response_model=FineTuningJob)
async def retrieve_fine_tuning_job(fine_tuning_job_id: str):
    job = fine_tuning_jobs.get(fine_tuning_job_id)
    if job is None:
        raise HTTPException(status_code=404, detail="Fine-tuning job not found")
    return job


# 4. List Fine-tuning Job Events
@app.get(
    "/v1/fine_tuning/jobs/{fine_tuning_job_id}/events",
    response_model=FineTuningEventList,
)
async def list_fine_tuning_events(fine_tuning_job_id: str, limit: Optional[int] = 20):
    if fine_tuning_job_id not in fine_tuning_jobs:
        raise HTTPException(status_code=404, detail="Fine-tuning job not found")
    events = fine_tuning_events.get(fine_tuning_job_id, [])
    return FineTuningEventList(
        object="list",
        data=events[:limit],
        has_more=len(events) > limit,
    )


class Message(BaseModel):
    role: str
    content: str


class CompletionRequest(BaseModel):
    model: str
    messages: List[Message]


class Choice(BaseModel):
    index: int
    message: Message
    finish_reason: str


class CompletionResponse(BaseModel):
    id: Optional[str] = None
    created: Optional[int] = None
    model: Optional[str] = None
    choices: Optional[List[Choice]] = None


# Perform inference
@app.post("/v1/chat/completions", response_model=CompletionResponse)
async def chat_completions(request: CompletionRequest):
    print(request)

    cfg = load_cfg("helix-mistral-instruct-v1.yml")
    cfg.sample_packing = False
    # TODO: parse job ID and get LORA file better
    if request.model != "":
        cfg["lora_model_dir"] = request.model
    cfg["chat_template"] = "mistral_v1"

    parser = transformers.HfArgumentParser((TrainerCliArgs))
    cli_args, _ = parser.parse_args_into_dataclasses(return_remaining_strings=True)
    cli_args.inference = True

    model, tokenizer = load_model_and_tokenizer(cfg=cfg, cli_args=cli_args)
    default_tokens = {"unk_token": "<unk>", "bos_token": "<s>", "eos_token": "</s>"}

    for token, symbol in default_tokens.items():
        # If the token isn't already specified in the config, add it
        if not (cfg.special_tokens and token in cfg.special_tokens):
            tokenizer.add_special_tokens({token: symbol})

    chat_template_str = None
    if cfg.chat_template:
        chat_template_str = chat_templates(cfg.chat_template)

    model = model.to(cfg.device, dtype=cfg.torch_dtype)

    print("=" * 80)

    batch = tokenizer.apply_chat_template(
        request.messages,
        return_tensors="pt",
        add_special_tokens=True,
        add_generation_prompt=True,
        chat_template=chat_template_str,
        tokenize=True,
        return_dict=True,
    )

    print("=" * 40)
    model.eval()
    with torch.no_grad():
        generation_config = GenerationConfig(
            repetition_penalty=1.1,
            max_new_tokens=1024,
            temperature=0.9,
            top_p=0.95,
            top_k=40,
            bos_token_id=tokenizer.bos_token_id,
            eos_token_id=tokenizer.eos_token_id,
            pad_token_id=tokenizer.pad_token_id,
            do_sample=True,
            use_cache=True,
            return_dict_in_generate=True,
            output_attentions=False,
            output_hidden_states=False,
            output_scores=False,
        )
        streamer = TextStreamer(tokenizer)
        generated_ids = model.generate(
            inputs=batch["input_ids"].to(cfg.device),
            attention_mask=batch["attention_mask"].to(cfg.device),
            generation_config=generation_config,
            streamer=streamer,
        )
    print("=" * 40)
    print(batch["input_ids"].shape[1])
    print(generated_ids["sequences"].shape)

    answer = tokenizer.decode(
        generated_ids["sequences"][:, batch["input_ids"].shape[-1] :].cpu().tolist()[0],
        skip_special_tokens=True,
    )
    print(answer.strip())

    return CompletionResponse(
        id="1",
        created=int(time.time()),
        model=request.model,
        choices=[
            Choice(
                index=0,
                message=Message(
                    role="system",
                    content=answer.strip(),
                ),
                finish_reason="complete",
            )
        ],
    )


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}
