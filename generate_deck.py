import os
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.dml.color import RGBColor
from pptx.enum.text import PP_ALIGN, MSO_ANCHOR
from pptx.enum.shapes import MSO_SHAPE

def create_deck():
    prs = Presentation()
    # 16:9 Widescreen standard
    prs.slide_width = Inches(13.333)
    prs.slide_height = Inches(7.5)

    blank_layout = prs.slide_layouts[6]

    # Color Palette - Vibrant, warm, kid-friendly & modern
    BG_DARK = RGBColor(24, 32, 54)        # Deep Navy for title & final slides
    BG_LIGHT = RGBColor(245, 247, 252)    # Soft cloud background
    CARD_BG = RGBColor(255, 255, 255)     # Card white
    
    PRIMARY_PURPLE = RGBColor(110, 68, 255) # Electric purple
    ACCENT_CYAN = RGBColor(0, 180, 216)     # Sky cyan
    ACCENT_ORANGE = RGBColor(255, 122, 0)   # Tangerine orange
    ACCENT_GREEN = RGBColor(16, 185, 129)   # Mint green
    ACCENT_PINK = RGBColor(236, 72, 153)    # Bubblegum pink
    ACCENT_YELLOW = RGBColor(245, 158, 11)  # Honey yellow
    
    TEXT_DARK = RGBColor(30, 41, 59)       # Slate dark
    TEXT_MUTED = RGBColor(100, 116, 139)   # Slate muted
    TEXT_LIGHT = RGBColor(255, 255, 255)

    def set_slide_bg(slide, color):
        bg = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, 0, 0, Inches(13.333), Inches(7.5))
        bg.fill.solid()
        bg.fill.fore_color.rgb = color
        bg.line.fill.background()
        return bg

    def add_header(slide, title_text, category_text="AI FOR KIDS 🤖", is_dark=False):
        # Category pill
        pill = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(0.45), Inches(2.3), Inches(0.35))
        pill.fill.solid()
        pill.fill.fore_color.rgb = PRIMARY_PURPLE if not is_dark else ACCENT_CYAN
        pill.line.fill.background()
        tf = pill.text_frame
        tf.vertical_anchor = MSO_ANCHOR.MIDDLE
        p = tf.paragraphs[0]
        p.text = category_text
        p.alignment = PP_ALIGN.CENTER
        p.font.name = "Arial"
        p.font.size = Pt(11)
        p.font.bold = True
        p.font.color.rgb = TEXT_LIGHT if not is_dark else BG_DARK

        # Title text
        tb = slide.shapes.add_textbox(Inches(0.8), Inches(0.85), Inches(11.7), Inches(0.8))
        tf_t = tb.text_frame
        tf_t.word_wrap = True
        p_t = tf_t.paragraphs[0]
        p_t.text = title_text
        p_t.font.name = "Arial"
        p_t.font.size = Pt(25)
        p_t.font.bold = True
        p_t.font.color.rgb = TEXT_LIGHT if is_dark else TEXT_DARK

    def add_card(slide, left, top, width, height, title, body_bullets, accent_color, bg_color=CARD_BG, icon="💡"):
        card = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(height))
        card.fill.solid()
        card.fill.fore_color.rgb = bg_color
        card.line.color.rgb = accent_color
        card.line.width = Pt(2)

        # Header bar
        bar = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left + 0.15), Inches(top + 0.15), Inches(width - 0.3), Inches(0.55))
        bar.fill.solid()
        bar.fill.fore_color.rgb = accent_color
        bar.line.fill.background()
        tf_bar = bar.text_frame
        tf_bar.vertical_anchor = MSO_ANCHOR.MIDDLE
        p_bar = tf_bar.paragraphs[0]
        p_bar.text = f"{icon}  {title}"
        p_bar.font.name = "Arial"
        p_bar.font.size = Pt(14)
        p_bar.font.bold = True
        p_bar.font.color.rgb = TEXT_LIGHT

        # Text Content
        tb = slide.shapes.add_textbox(Inches(left + 0.2), Inches(top + 0.8), Inches(width - 0.4), Inches(height - 0.95))
        tf = tb.text_frame
        tf.word_wrap = True
        for i, item in enumerate(body_bullets):
            p = tf.add_paragraph() if i > 0 else tf.paragraphs[0]
            p.text = ("• " if not any(item.startswith(x) for x in ["👉", "⚠️", "✅", "❌", "🔍", "🏆", "1.", "2.", "3.", "❓", "•"]) and item.strip() != "" else "") + item
            p.font.name = "Arial"
            p.font.size = Pt(13)
            p.font.color.rgb = TEXT_DARK
            p.space_after = Pt(6)

    # ----------------------------------------------------
    # SLIDE 1: TITLE SLIDE (Dark & Magical)
    # ----------------------------------------------------
    s1 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s1, BG_DARK)
    
    badge = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(4.66), Inches(1.2), Inches(4.0), Inches(0.45))
    badge.fill.solid()
    badge.fill.fore_color.rgb = ACCENT_PINK
    badge.line.fill.background()
    p = badge.text_frame.paragraphs[0]
    p.text = "✨ SUPER SMART COMPUTERS ✨"
    p.alignment = PP_ALIGN.CENTER
    p.font.name = "Arial"
    p.font.bold = True
    p.font.size = Pt(12)
    p.font.color.rgb = TEXT_LIGHT

    tb = s1.shapes.add_textbox(Inches(1.0), Inches(1.9), Inches(11.333), Inches(2.2))
    tf = tb.text_frame
    tf.word_wrap = True
    p1 = tf.paragraphs[0]
    p1.text = "How Do Robots Think?"
    p1.alignment = PP_ALIGN.CENTER
    p1.font.name = "Arial Black"
    p1.font.size = Pt(42)
    p1.font.color.rgb = TEXT_LIGHT

    p2 = tf.add_paragraph()
    p2.text = "A Kid's Secret Guide to AI, LLMs & Robot Brains 🤖🧠"
    p2.alignment = PP_ALIGN.CENTER
    p2.font.name = "Arial"
    p2.font.size = Pt(22)
    p2.font.bold = True
    p2.font.color.rgb = ACCENT_CYAN
    p2.space_before = Pt(12)

    p_tags = [
        ("🧩 Tokens & Words", "How AI breaks down language", Inches(1.5), ACCENT_ORANGE),
        ("🧠 Simulated Brains", "Why it acts like it's thinking", Inches(5.16), PRIMARY_PURPLE),
        ("💻 Robot Code & Math", "Solving puzzles & games", Inches(8.83), ACCENT_GREEN)
    ]
    for tag, desc, pos_x, col in p_tags:
        box = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, pos_x, Inches(4.6), Inches(3.0), Inches(1.8))
        box.fill.solid()
        box.fill.fore_color.rgb = RGBColor(36, 48, 80)
        box.line.color.rgb = col
        box.line.width = Pt(2)
        tf_b = box.text_frame
        tf_b.word_wrap = True
        p_h = tf_b.paragraphs[0]
        p_h.text = tag
        p_h.alignment = PP_ALIGN.CENTER
        p_h.font.name = "Arial"
        p_h.font.bold = True
        p_h.font.size = Pt(15)
        p_h.font.color.rgb = col
        p_h.space_before = Pt(14)
        
        p_d = tf_b.add_paragraph()
        p_d.text = desc
        p_d.alignment = PP_ALIGN.CENTER
        p_d.font.name = "Arial"
        p_d.font.size = Pt(12)
        p_d.font.color.rgb = RGBColor(200, 210, 230)
        p_d.space_before = Pt(8)

    # ----------------------------------------------------
    # SLIDE 2: WHAT IS AN LLM?
    # ----------------------------------------------------
    s2 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s2, BG_LIGHT)
    add_header(s2, "What is an LLM? (Large Language Model) 📚")

    add_card(s2, 0.8, 1.8, 3.6, 5.0, "The Giant Reader", [
        "Imagine reading EVERY book in the world!",
        "An LLM read billions of stories, websites, and schoolbooks.",
        "It spots patterns in how human words fit together.",
        "👉 It doesn't have eyes or feelings—just lots of words!"
    ], PRIMARY_PURPLE, icon="📖")

    add_card(s2, 4.85, 1.8, 3.6, 5.0, "Lego Bricks of Words", [
        "Computers don't see full words like we do.",
        "They chop sentences into 'Tokens' (puzzle pieces).",
        "Example: 'Butter' + 'fly' = Butterfly 🦋",
        "👉 Each piece gets its own secret number code."
    ], ACCENT_CYAN, icon="🧱")

    add_card(s2, 8.9, 1.8, 3.6, 5.0, "The Great Guessing Game", [
        "When you ask a question, the AI asks itself:",
        "'What word makes the most sense NEXT?'",
        "It chains high-scoring guesses one after another.",
        "👉 Result: A super smart sounding sentence!"
    ], ACCENT_ORANGE, icon="🎯")

    # ----------------------------------------------------
    # SLIDE 3: HOW AI GOES TO SCHOOL
    # ----------------------------------------------------
    s3 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s3, BG_LIGHT)
    add_header(s3, "How AI Goes to School! 🎒 (The 3 Steps)")

    add_card(s3, 0.8, 1.8, 3.6, 5.0, "Step 1: Pre-training 🏫", [
        "The Kindergarten to College phase!",
        "Reads massive libraries of text.",
        "Learns grammar, facts, math, and logic patterns.",
        "🔑 This is where core reasoning ability is built!"
    ], ACCENT_ORANGE, icon="1️⃣")

    add_card(s3, 4.85, 1.8, 3.6, 5.0, "Step 2: Good Manners (RLHF) 👩‍🏫", [
        "Human teachers give high-fives or thumbs down.",
        "Teaches the AI to be polite, safe, and helpful.",
        "Trains it to show work step-by-step!",
        "🔑 Shapes the AI's friendly, helpful teaching style."
    ], PRIMARY_PURPLE, icon="2️⃣")

    add_card(s3, 8.9, 1.8, 3.6, 5.0, "Step 3: Your Prompt! 💬", [
        "The conversation you are having right now!",
        "AI applies everything it learned to YOUR question.",
        "No permanent changes happen to its brain.",
        "🔑 It works like a temporary whiteboard puzzle!"
    ], ACCENT_GREEN, icon="3️⃣")

    # ----------------------------------------------------
    # SLIDE 4: REAL BRAIN vs AI BRAIN
    # ----------------------------------------------------
    s4 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s4, BG_LIGHT)
    add_header(s4, "Who is Smarter? Real Brain vs. AI Brain 🧠🤖")

    add_card(s4, 0.8, 1.8, 5.6, 5.0, "Human Kids (YOU!) 👧👦", [
        "✅ Truly UNDERSTANDS what an apple tastes like 🍎",
        "✅ Has real feelings, empathy, and dreams 💖",
        "✅ Creates brand new wild ideas from imagination 🎨",
        "✅ Learns from just 1 or 2 examples without needing a million books!",
        "🏆 Champion of common sense and real creativity!"
    ], ACCENT_PINK, icon="🌟")

    add_card(s4, 6.8, 1.8, 5.7, 5.0, "AI / LLMs 🤖⚡", [
        "⚡ Super fast at reading 1,000 pages in 1 second!",
        "🧩 Simulates thinking using word probabilities",
        "⚠️ Doesn't actually know what 'warm' or 'sweet' feels like",
        "⚠️ Can get tricked if you ask weird upside-down questions",
        "🛠️ A super-helpful calculator for words, code & ideas!"
    ], ACCENT_CYAN, icon="🤖")

    # ----------------------------------------------------
    # SLIDE 5: LOGIC PUZZLE (ALICE, BOB, CHARLIE)
    # ----------------------------------------------------
    s5 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s5, BG_LIGHT)
    add_header(s5, "Detective Challenge: The Height Riddle! 🕵️‍♂️")

    add_card(s5, 0.8, 1.8, 5.6, 5.0, "The Riddle & Clues 📜", [
        "❓ The Mystery Question:",
        "\"If Alice is taller than Bob, and Bob is taller than Charlie, who is the tallest?\"",
        "",
        "🔍 Clue 1: Alice > Bob",
        "🔍 Clue 2: Bob > Charlie",
        "🔍 Logic Chain: Alice > Charlie!",
        "🏆 The Answer: Alice is the tallest!"
    ], PRIMARY_PURPLE, icon="❓")

    add_card(s5, 6.8, 1.8, 5.7, 5.0, "How Did the AI Solve It? 🤖", [
        "1. Logic Rule: Learned transitive order (A > B > C) in pre-training.",
        "2. Step-by-Step: RLHF taught it to explain each clue clearly.",
        "3. Who is Alice?: Recognizes 'Alice' and 'Bob' are names of people from story patterns.",
        "⚠️ Remember: It connects statistical patterns, not physical people standing in a room!"
    ], ACCENT_GREEN, icon="💡")

    # ----------------------------------------------------
    # SLIDE 6: MATH PROBLEM (TWO TRAINS)
    # ----------------------------------------------------
    s6 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s6, BG_LIGHT)
    add_header(s6, "Math Mystery: When Do The Trains Meet? 🚂💨")

    add_card(s6, 0.8, 1.8, 5.6, 5.0, "The Train Problem 🛤️", [
        "• Train A leaves City 1 at 2:00 PM (Speed: 60 km/h)",
        "• Train B leaves City 2 at 3:00 PM (Speed: 90 km/h)",
        "• Total distance between cities = 300 km.",
        "",
        "🧮 Step-by-Step Solution:",
        "1. Hour 1: Train A travels 60 km (240 km left).",
        "2. Combined speed = 60 + 90 = 150 km/h.",
        "3. Time = 240 / 150 = 1.6 hrs (1 hr 36 mins).",
        "✅ Meet Time: 3:00 PM + 1h36m = 4:36 PM! 🎉"
    ], ACCENT_CYAN, icon="🚂")

    add_card(s6, 6.8, 1.8, 5.7, 5.0, "Where AI Sometimes Gets Tricked! ⚠️", [
        "Watch out! AI can make funny math mistakes:",
        "",
        "❌ Time Confusion: Thinking 0.1 hours = 10 minutes (it's actually 6 minutes!).",
        "❌ Skipping Steps: Forgetting that Train B started an hour later.",
        "❌ Rounding Mistakes: Dropping fractions too early.",
        "💡 Pro Tip: Always double-check AI calculations yourself!"
    ], ACCENT_ORANGE, icon="⚠️")

    # ----------------------------------------------------
    # SLIDE 7: CODE GENERATION
    # ----------------------------------------------------
    s7 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s7, BG_LIGHT)
    add_header(s7, "How AI Writes Code: Simple vs. Superhero Mode! 💻🦸")

    add_card(s7, 0.8, 1.8, 5.6, 5.0, "Level 1: Beginner Code 🐣", [
        "Prompt: \"Write a Python function to divide two numbers.\"",
        "",
        "💻 Python Code:",
        "def divide(a, b):",
        "    return a / b",
        "",
        "⚠️ The Trap: What if someone divides by 0? Or enters the word 'Pizza' instead of a number?",
        "💥 Result: The computer program crashes!"
    ], ACCENT_PINK, icon="🐣")

    add_card(s7, 6.8, 1.8, 5.7, 5.0, "Level 2: Superhero Code 🛡️", [
        "Prompt: \"Write divide function handling edge cases.\"",
        "",
        "💻 Smart Python Code:",
        "def divide(a: float, b: float):",
        "    if not isinstance(a, (int, float)):",
        "        raise TypeError('Numbers only!')",
        "    if b == 0:",
        "        raise ValueError('Cannot divide by zero!')",
        "    return a / b",
        "",
        "✅ Safe, crash-proof, and super smart!"
    ], ACCENT_GREEN, icon="🦸‍♂️")

    # ----------------------------------------------------
    # SLIDE 8: DOES AI RUN CODE?
    # ----------------------------------------------------
    s8 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s8, BG_LIGHT)
    add_header(s8, "Behind the Curtain: Does AI Run Code? 🧪🎭")

    add_card(s8, 0.8, 1.8, 3.6, 5.0, "Simulated Testing 🎭", [
        "When AI writes a test:",
        "assertEqual(calc.add(2, 3), 5)",
        "",
        "It doesn't actually turn on Python to run it!",
        "👉 It writes what it predicts a good test looks like."
    ], PRIMARY_PURPLE, icon="🎭")

    add_card(s8, 4.85, 1.8, 3.6, 5.0, "Post-Hoc Stories 💬", [
        "When an AI explains 'Why' it gave an answer:",
        "It generates a story after picking words!",
        "The real math happened invisibly via numbers.",
        "👉 Like a magician narrating their card trick!"
    ], ACCENT_ORANGE, icon="🎩")

    add_card(s8, 8.9, 1.8, 3.6, 5.0, "Humans in Charge 👨‍✈️", [
        "Because AI doesn't run its own code:",
        "• Humans must run & test it.",
        "• Humans fix the sneaky bugs.",
        "• Humans decide what to create.",
        "👉 You are the director, AI is the actor!"
    ], ACCENT_GREEN, icon="🚀")

    # ----------------------------------------------------
    # SLIDE 9: 3 GOLDEN RULES
    # ----------------------------------------------------
    s9 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s9, BG_DARK)
    add_header(s9, "3 Golden Rules for Junior AI Explorers! 🌟", "AI RULES 🏆", is_dark=True)

    rules = [
        ("Rule 1: Be the Boss Detective 🔍", "Never believe an AI 100% without checking! AI can hallucinate or get confused by tricky riddles. You are the fact-checker!", Inches(0.8), ACCENT_ORANGE),
        ("Rule 2: Give Super Clear Prompts 🎯", "Garbage in = Garbage out! If you give clear, detailed instructions, the AI finds much better puzzle pieces to help you.", Inches(4.85), ACCENT_CYAN),
        ("Rule 3: Keep Your Human Spark 💡", "AI has memory of old words, but YOU have imagination for new inventions. Use AI as your superpower tool to build the future!", Inches(8.9), ACCENT_GREEN)
    ]

    for title, text, x_pos, col in rules:
        card = s9.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos, Inches(1.8), Inches(3.6), Inches(5.0))
        card.fill.solid()
        card.fill.fore_color.rgb = RGBColor(36, 48, 80)
        card.line.color.rgb = col
        card.line.width = Pt(2.5)

        # Header tag
        tag = s9.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos + Inches(0.2), Inches(2.0), Inches(3.2), Inches(0.6))
        tag.fill.solid()
        tag.fill.fore_color.rgb = col
        tag.line.fill.background()
        tf_t = tag.text_frame
        tf_t.vertical_anchor = MSO_ANCHOR.MIDDLE
        p_t = tf_t.paragraphs[0]
        p_t.text = title
        p_t.alignment = PP_ALIGN.CENTER
        p_t.font.name = "Arial"
        p_t.font.size = Pt(13)
        p_t.font.bold = True
        p_t.font.color.rgb = TEXT_LIGHT if col != ACCENT_CYAN else BG_DARK

        # Body
        tb = s9.shapes.add_textbox(x_pos + Inches(0.2), Inches(2.8), Inches(3.2), Inches(3.8))
        tf = tb.text_frame
        tf.word_wrap = True
        p = tf.paragraphs[0]
        p.text = text
        p.font.name = "Arial"
        p.font.size = Pt(14)
        p.font.color.rgb = RGBColor(220, 230, 245)
        p.space_before = Pt(8)

    output_path = "c:/WebSearch_GO/How_AI_Thinks_Kids_Guide.pptx"
    prs.save(output_path)
    print(f"Presentation saved successfully to {output_path}")

if __name__ == "__main__":
    create_deck()
