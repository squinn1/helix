import asyncio
import logging
import os
import tempfile
import traceback
import uuid
from contextlib import asynccontextmanager
from typing import List

import diffusers
import PIL
import torch
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
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
        logging.info("Pipeline instance created")

    def start(self, model_id: str):
        logging.info(f"Starting pipeline for model {model_id}, cache dir: {cache_dir}")
        try:
            if torch.cuda.is_available():
                logger.info("Loading CUDA")
                self.device = "cuda"
                self.pipeline = diffusers.AutoPipelineForText2Image.from_pretrained(
                    model_id,
                    torch_dtype=torch.bfloat16,
                    local_files_only=True,
                    cache_dir=cache_dir,
                ).to(device=self.device)
            elif torch.backends.mps.is_available():
                logger.info("Loading MPS for Mac M Series")
                self.device = "mps"
                self.pipeline = diffusers.AutoPipelineForText2Image.from_pretrained(
                    model_id,
                    torch_dtype=torch.bfloat16,
                    local_files_only=True,
                    cache_dir=cache_dir,
                ).to(device=self.device)
            else:
                raise Exception("No CUDA or MPS device available")
            logging.info("Pipeline successfully initialized")
        except Exception as e:
            logging.error(f"Failed to initialize pipeline: {e}")
            raise

    def generate(self, prompt: str) -> List[PIL.Image.Image]:
        logging.info(f"Generate called with pipeline state: {self.pipeline is not None}")
        if self.pipeline is None:
            raise RuntimeError("Pipeline not initialized. Call start() before generate()")

        try:
            # Validate scheduler configuration
            if not hasattr(self.pipeline, "scheduler"):
                raise RuntimeError("Pipeline scheduler not properly configured")

            scheduler = self.pipeline.scheduler.from_config(self.pipeline.scheduler.config)
            self.pipeline.scheduler = scheduler

            return self.pipeline(prompt=prompt, num_inference_steps=50, guidance_scale=7.5, height=720, width=1280).images

        except Exception as e:
            raise RuntimeError(f"Error during image generation: {str(e)}") from e


@asynccontextmanager
async def lifespan(app: FastAPI):
    yield
    shared_pipeline.stop()

app = FastAPI(lifespan=lifespan)
image_dir = os.path.join(tempfile.gettempdir(), "images")
if not os.path.exists(image_dir):
    os.makedirs(image_dir)
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


def save_image(image):
    filename = "draw" + str(uuid.uuid4()).split("-")[0] + ".png"
    image_path = os.path.join(image_dir, filename)
    # write image to disk at image_path
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
    
    Args:
        model_name (str): Name of the model on Hugging Face Hub
        save_path (str): Local directory path to save the model
        pipeline_type (str): Type of pipeline to use ('sd' for StableDiffusion or 'flux' for Flux)
    """
    print(f"Downloading model: {model_name}")
    
    # Download all model files directly without pipeline initialization
    snapshot_download(
        repo_id=model_name,
        cache_dir=save_path,
    )

    # Check the location of the downloaded models
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
    # Read all of the models in the cache dir
    models = os.listdir(cache_dir)
    
    # Convert to ListModelsResponse
    return ListModelsResponse(models=[Model(CreatedAt=0, ID=model, Object="model", OwnedBy="helix", Permission=[], Root="", Parent="") for model in models])


class WarmRequest(BaseModel):
    model: str

@app.post("/warm")
async def warm(warm_request: WarmRequest):
    shared_pipeline.start(warm_request.model)
    return {"status": "ok"}

@app.post("/v1/images/generations")
async def generate_image(image_input: TextToImageInput):
    try:
        if shared_pipeline.pipeline is None:
            raise RuntimeError("Pipeline not initialized. Please try again in a few moments.")

        logger.info(f"generate_image called with prompt: {image_input.prompt}")
        loop = asyncio.get_running_loop()
        output = await loop.run_in_executor(
            None, lambda: shared_pipeline.generate(image_input.prompt)
        )
        logger.info(f"output: {output}")
        image_path, image_url = save_image(output[0])
        print(f"image_path: {image_path}, image_url: {image_url}")
        # TODO(Phil): This is a hack to allow the runner to upload the image to the filestore,
        # figure out a better way, e.g. a fileserver.
        return {"data": [{"url": image_path}]}
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"{str(e)}\nTraceback (most recent call last):\n{traceback.format_exc()}",
        )

if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host=server_host, port=server_port)
