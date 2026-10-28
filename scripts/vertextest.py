from google import genai

try:
    client = genai.Client(
        vertexai=True,
        project="gen-lang-client-0665888431",
        location="global" #"us-central1"
    )

    response = client.models.generate_content(
        model="gemma-4-26b-a4b-it-maas",
        contents="Say hello!",
    )
    print("Success! Response:")
    print(response.text)
except Exception as e:
    print("Failed with error:")
    print(e)
