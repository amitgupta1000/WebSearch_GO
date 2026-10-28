"""Minimal Vertex AI Gemini connectivity test.

Authentication uses Application Default Credentials:
    gcloud auth application-default login

Example:
    python scripts/test_vertexai.py --model gemini-2.0-flash
"""

from __future__ import annotations

import argparse
import os
import sys


def main() -> int:
    parser = argparse.ArgumentParser(description="Test Gemini through Vertex AI")
    parser.add_argument(
        "--model",
        default=os.getenv("GEMINI_MODEL", "gemini-2.0-flash"),
        help="Vertex publisher model name",
    )
    parser.add_argument(
        "--project",
        default=os.getenv("GOOGLE_CLOUD_PROJECT"),
        help="Google Cloud project ID",
    )
    parser.add_argument(
        "--location",
        default=os.getenv("GOOGLE_CLOUD_LOCATION", "us-central1"),
        help="Vertex AI location",
    )
    args = parser.parse_args()

    if not args.project:
        print("GOOGLE_CLOUD_PROJECT is required", file=sys.stderr)
        return 2

    try:
        from google import genai
    except ImportError:
        print(
            "Missing dependency: google-genai. Install it in the project "
            "environment before running this probe.",
            file=sys.stderr,
        )
        return 2

    print(f"Vertex AI project: {args.project}")
    print(f"Vertex AI location: {args.location}")
    print(f"Model: {args.model}")

    try:
        client = genai.Client(
            vertexai=True,
            project=args.project,
            location=args.location,
        )
        response = client.models.generate_content(
            model=args.model,
            contents="Reply with exactly: VERTEX_AI_OK",
        )
        print("Response:", response.text.strip() if response.text else "<empty>")
        return 0
    except Exception as exc:
        print(f"Vertex AI request failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
