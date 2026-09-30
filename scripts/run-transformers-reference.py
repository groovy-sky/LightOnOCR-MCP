#!/usr/bin/env python3
import argparse
import json
from pathlib import Path

import torch
from PIL import Image
from transformers import LightOnOcrForConditionalGeneration, LightOnOcrProcessor

parser = argparse.ArgumentParser()
parser.add_argument("--model", required=True)
parser.add_argument("--fixtures", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--max-tokens", type=int, default=4096)
args = parser.parse_args()

model = LightOnOcrForConditionalGeneration.from_pretrained(args.model, torch_dtype=torch.float32).eval()
processor = LightOnOcrProcessor.from_pretrained(args.model)
results = {}
for image_path in sorted(Path(args.fixtures).glob("*.png")):
    conversation = [{"role": "user", "content": [{"type": "image", "image": Image.open(image_path)}]}]
    inputs = processor.apply_chat_template(conversation, add_generation_prompt=True, tokenize=True, return_dict=True, return_tensors="pt")
    with torch.inference_mode():
        output = model.generate(**inputs, max_new_tokens=args.max_tokens, do_sample=False)
    generated = output[0, inputs["input_ids"].shape[1]:]
    results[image_path.name] = processor.decode(generated, skip_special_tokens=True)
Path(args.output).write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
