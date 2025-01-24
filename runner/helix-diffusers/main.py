import asyncio
import logging
import os
import tempfile
import uuid
from contextlib import asynccontextmanager
from datetime import datetime
from typing import Callable, Dict, List, Optional

import diffusers
import PIL
import torch
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from huggingface_hub import snapshot_download
from pydantic import BaseModel

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)

server_host = os.getenv("SERVER_HOST", "0.0.0.0")
server_port = int(os.getenv("SERVER_PORT", 8000))
server_url = f"http://{server_host}:{server_port}"
cache_dir = os.getenv("CACHE_DIR", "/root/.cache/huggingface/hub")

# Check that the cache dir exists
if not os.path.exists(cache_dir):
    raise RuntimeError(f"Cache directory {cache_dir} does not exist")


class TextToImageInput(BaseModel):
    model: str
    prompt: str
    size: str | None = None
    n: int | None = None

class TextToImagePipeline:
    def __init__(self):
        self.pipeline = None
        self.device = None
        logging.info("Pipeline instance created")

    def start(self, model_id: str):
        logging.info(f"Starting pipeline for model {model_id}, cache dir: {cache_dir}")
        try:
            if torch.cuda.is_available():
                logger.info("Loading CUDA")
                self.device = "cuda"
                self.pipeline = diffusers.StableDiffusionPipeline.from_pretrained(
                    model_id,
                    torch_dtype=torch.bfloat16,
                    local_files_only=True,
                    cache_dir=cache_dir,
                    safety_checker=None,  # Explicitly disable safety checker
                    requires_safety_checker=False,
                ).to(device=self.device)
            elif torch.backends.mps.is_available():
                logger.info("Loading MPS for Mac M Series")
                self.device = "mps"
                self.pipeline = diffusers.StableDiffusionPipeline.from_pretrained(
                    model_id,
                    torch_dtype=torch.bfloat16,
                    local_files_only=True,
                    cache_dir=cache_dir,
                    safety_checker=None,  # Explicitly disable safety checker
                    requires_safety_checker=False,
                ).to(device=self.device)
            else:
                raise Exception("No CUDA or MPS device available")

            # Verify pipeline components
            logger.info("Verifying pipeline components...")
            logger.info(f"VAE loaded: {self.pipeline.vae is not None}")
            logger.info(f"UNet loaded: {self.pipeline.unet is not None}")
            logger.info(f"Text Encoder loaded: {self.pipeline.text_encoder is not None}")
            logger.info(f"Tokenizer loaded: {self.pipeline.tokenizer is not None}")
            logger.info(f"Scheduler loaded: {self.pipeline.scheduler is not None}")

            logging.info("Pipeline successfully initialized")

        except Exception as e:
            logging.error(f"Failed to initialize pipeline: {e}")
            raise

    def generate(
        self,
        prompt: str,
        callback: Optional[Callable[[diffusers.DiffusionPipeline, int, int, Dict], None]] = None,
    ) -> List[PIL.Image.Image]:
        """Generate images, optionally with a step callback.

        Args:
            prompt (str): The text prompt for image generation.
            callback (callable): 
        """
        logging.info(f"Generate called with pipeline state: {self.pipeline is not None}")
        if self.pipeline is None:
            raise RuntimeError("Pipeline not initialized. Call start() before generate()")

        try:
            # Add more detailed logging
            logger.info(f"Pipeline scheduler type: {type(self.pipeline.scheduler)}")
            logger.info(f"Pipeline scheduler config: {self.pipeline.scheduler.config if hasattr(self.pipeline, 'scheduler') else 'No scheduler'}")

            # Remove the scheduler recreation since sd-turbo comes with its own optimized scheduler
            result = self.pipeline(
                prompt=prompt,
                num_inference_steps=50,
                guidance_scale=7.5,
                height=720,
                width=1280,
                callback_on_step_end=callback,
            )
            return result.images

        except Exception as e:
            logger.error(f"Error during image generation: {str(e)}")
            logger.error(f"Pipeline state: {self.pipeline}")
            raise RuntimeError(f"Error during image generation: {str(e)}") from e


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Any startup logic here
    yield
    # No shared_pipeline.stop() since we don't have a stop() method
    # If you do need teardown logic, define a .stop() or similar.
    

app = FastAPI(lifespan=lifespan)
image_dir = os.path.join(tempfile.gettempdir(), "images")
os.makedirs(image_dir, exist_ok=True)
app.mount("/images", StaticFiles(directory=image_dir), name="images")

shared_pipeline = TextToImagePipeline()

# Configure CORS settings
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # Allows all origins
    allow_credentials=True,
    allow_methods=["*"],  # Allows all methods, e.g., GET, POST, OPTIONS, etc.
    allow_headers=["*"],  # Allows all headers
)


def save_image(image: PIL.Image.Image):
    filename = "draw" + str(uuid.uuid4()).split("-")[0] + ".png"
    image_path = os.path.join(image_dir, filename)
    logger.info(f"Saving image to {image_path}")
    image.save(image_path)
    return image_path, os.path.join(server_url, "images", filename)


@app.get("/healthz")
async def base():
    return {"status": "ok"}


@app.get("/version")
async def version():
    return {"version": diffusers.__version__}


class PullRequest(BaseModel):
    model: str


@app.post("/pull")
async def pull(pull_request: PullRequest):
    download_model(pull_request.model, cache_dir)
    return {"status": "ok"}


def download_model(model_name: str, save_path: str):
    """
    Download model weights from Hugging Face Hub
    """
    print(f"Downloading model: {model_name}")
    snapshot_download(repo_id=model_name, cache_dir=save_path)
    print(f"Model successfully downloaded to: {save_path}")


class Model(BaseModel):
    CreatedAt: int
    ID: str
    Object: str
    OwnedBy: str
    Permission: List[str]
    Root: str
    Parent: str


class ListModelsResponse(BaseModel):
    models: List[Model]


@app.get("/v1/models", response_model=ListModelsResponse)
async def list_models():
    models = os.listdir(cache_dir)
    return ListModelsResponse(
        models=[
            Model(
                CreatedAt=0,
                ID=model,
                Object="model",
                OwnedBy="helix",
                Permission=[],
                Root="",
                Parent="",
            )
            for model in models
        ]
    )


class WarmRequest(BaseModel):
    model: str


@app.post("/warm")
async def warm(warm_request: WarmRequest):
    shared_pipeline.start(warm_request.model)
    return {"status": "ok"}


class ImageResponseDataInner(BaseModel):
    url: str
    b64_json: str
    revised_prompt: str


class ImageResponse(BaseModel):
    created: int
    step: int
    timestep: int
    error: str
    completed: bool
    data: List[ImageResponseDataInner]


async def stream_progress(prompt: str):
    """Coroutine that yields SSE data while generating images in background."""
    loop = asyncio.get_event_loop()
    progress_queue = asyncio.Queue()

    def diffusion_callback(pipe: diffusers.DiffusionPipeline, step: int, timestep: int, kwargs: Dict):
        try:
            # Construct progress object with safe defaults
            progress = ImageResponse(
                created=int(datetime.now().timestamp()),
                step=step if step is not None else 0,
                timestep=int(timestep) if timestep is not None else 0,
                error="",
                completed=False,
                data=[],  # Empty list since we don't have intermediate images
            )
            # Safely put it in the queue
            loop.call_soon_threadsafe(
                progress_queue.put_nowait,
                progress.model_dump_json()
            )
        except Exception as e:
            logger.error(f"Error in callback: {str(e)}")
            # Don't re-raise, just log, to prevent breaking the pipeline

    try:
        # Launch generation in separate thread
        generation_task = asyncio.create_task(
            asyncio.to_thread(
                shared_pipeline.generate,
                prompt,
                diffusion_callback
            )
        )

        while not generation_task.done():
            try:
                progress_json = await asyncio.wait_for(progress_queue.get(), timeout=0.2)
                yield f"data: {progress_json}\n\n"
            except asyncio.TimeoutError:
                pass

        # Get final images
        images = await generation_task
        urls = []
        for im in images:
            _, url = save_image(im)
            urls.append(url)  # Use the URL instead of path

        final_response = ImageResponse(
            created=int(datetime.now().timestamp()),
            step=50,  # Final step
            timestep=0,
            error="",
            completed=True,
            data=[ImageResponseDataInner(url=u, b64_json="", revised_prompt="") for u in urls]
        )
        yield f"data: {final_response.model_dump_json()}\n\n"

    except Exception as e:
        logger.error(f"Error in stream_progress: {str(e)}")
        error_response = ImageResponse(
            created=int(datetime.now().timestamp()),
            step=0,
            timestep=0,
            error=str(e),
            completed=True,
            data=[],
        )
        yield f"data: {error_response.model_dump_json()}\n\n"


@app.post("/v1/images/generations/stream")
async def generate_image_stream(image_input: TextToImageInput):
    if shared_pipeline.pipeline is None:
        raise RuntimeError("Pipeline not initialized. Please warm up the model first.")

    logger.info(f"generate_image_stream called with prompt: {image_input.prompt}")
    return StreamingResponse(
        stream_progress(image_input.prompt),
        media_type="text/event-stream",
        # Often recommended headers for SSE:
        # headers={"X-Accel-Buffering": "no", "Cache-Control": "no-cache"},
    )


@app.post("/v1/images/generations")
async def generate_image(image_input: TextToImageInput):
    """Blocking call that waits for the final image and returns."""
    try:
        if shared_pipeline.pipeline is None:
            raise RuntimeError("Pipeline not initialized. Please warm up the model first.")

        logger.info(f"generate_image called with prompt: {image_input.prompt}")
        # Offload to a thread so the main event loop is not blocked
        loop = asyncio.get_running_loop()
        images = await loop.run_in_executor(
            None, lambda: shared_pipeline.generate(image_input.prompt)
        )
        logger.info(f"Generated {len(images)} image(s).")

        # Save the first image to disk for illustration
        image_path, image_url = save_image(images[0])
        logger.info(f"Saved image to: {image_path}, accessible at: {image_url}")

        # Return path or URL. Up to you how you want to handle final return
        return {"data": [{"url": image_url}]}

    except Exception as e:
        logger.error(f"Error during image generation: {str(e)}")
        return JSONResponse(
            status_code=500,
            content={"error": {"code": "500", "message": str(e)}},
        )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host=server_host, port=server_port)