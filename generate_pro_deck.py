import os
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.dml.color import RGBColor
from pptx.enum.text import PP_ALIGN, MSO_ANCHOR
from pptx.enum.shapes import MSO_SHAPE

def create_pro_deck():
    prs = Presentation()
    prs.slide_width = Inches(13.333)
    prs.slide_height = Inches(7.5)
    blank_layout = prs.slide_layouts[6]

    # Professional Executive Color Palette
    BG_DARK = RGBColor(15, 23, 42)         # Slate 900 (Deep Executive Navy)
    BG_LIGHT = RGBColor(248, 250, 252)     # Slate 50 (Clean professional off-white)
    CARD_BG = RGBColor(255, 255, 255)      # Pure White
    CARD_BORDER = RGBColor(226, 232, 240)  # Slate 200
    
    PRIMARY_BLUE = RGBColor(29, 78, 216)   # Deep Royal Blue (Primary Accent)
    ACCENT_TEAL = RGBColor(13, 148, 136)   # Professional Teal
    ACCENT_INDIGO = RGBColor(79, 70, 229)  # Indigo
    ACCENT_AMBER = RGBColor(217, 119, 6)   # Warning / Insight Amber
    ACCENT_ROSE = RGBColor(225, 29, 72)    # Pitfall / Failure Red
    ACCENT_SLATE = RGBColor(71, 85, 105)   # Neutral Slate

    TEXT_DARK = RGBColor(15, 23, 42)       # Slate 900
    TEXT_BODY = RGBColor(51, 65, 85)       # Slate 700
    TEXT_MUTED = RGBColor(100, 116, 139)   # Slate 500
    TEXT_LIGHT = RGBColor(255, 255, 255)

    def set_slide_bg(slide, color):
        bg = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, 0, 0, Inches(13.333), Inches(7.5))
        bg.fill.solid()
        bg.fill.fore_color.rgb = color
        bg.line.fill.background()
        return bg

    def add_header(slide, title_text, category_text="TECHNICAL ARCHITECTURE & COGNITIVE ANALYSIS", is_dark=False):
        # Category label
        cat_box = slide.shapes.add_textbox(Inches(0.8), Inches(0.4), Inches(11.7), Inches(0.3))
        tf_c = cat_box.text_frame
        p_c = tf_c.paragraphs[0]
        p_c.text = category_text.upper()
        p_c.font.name = "Arial"
        p_c.font.size = Pt(10)
        p_c.font.bold = True
        p_c.font.color.rgb = PRIMARY_BLUE if not is_dark else RGBColor(147, 197, 253)

        # Main Title
        title_box = slide.shapes.add_textbox(Inches(0.8), Inches(0.65), Inches(11.7), Inches(0.7))
        tf_t = title_box.text_frame
        tf_t.word_wrap = True
        p_t = tf_t.paragraphs[0]
        p_t.text = title_text
        p_t.font.name = "Arial"
        p_t.font.size = Pt(22)
        p_t.font.bold = True
        p_t.font.color.rgb = TEXT_LIGHT if is_dark else TEXT_DARK

        # Underline divider
        line = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.38), Inches(11.733), Inches(0.02))
        line.fill.solid()
        line.fill.fore_color.rgb = RGBColor(51, 65, 85) if is_dark else CARD_BORDER
        line.line.fill.background()

    def add_card(slide, left, top, width, height, title, items, top_color=PRIMARY_BLUE, bg_color=CARD_BG, is_code=False):
        # Base container
        card = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(height))
        card.fill.solid()
        card.fill.fore_color.rgb = bg_color
        card.line.color.rgb = CARD_BORDER
        card.line.width = Pt(1)

        # Top Accent stripe
        stripe = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(0.08))
        stripe.fill.solid()
        stripe.fill.fore_color.rgb = top_color
        stripe.line.fill.background()

        # Header Title
        tb_h = slide.shapes.add_textbox(Inches(left + 0.25), Inches(top + 0.15), Inches(width - 0.5), Inches(0.45))
        tf_h = tb_h.text_frame
        p_h = tf_h.paragraphs[0]
        p_h.text = title
        p_h.font.name = "Arial"
        p_h.font.size = Pt(13)
        p_h.font.bold = True
        p_h.font.color.rgb = TEXT_DARK

        # Body items
        tb = slide.shapes.add_textbox(Inches(left + 0.25), Inches(top + 0.65), Inches(width - 0.5), Inches(height - 0.8))
        tf = tb.text_frame
        tf.word_wrap = True

        for i, item in enumerate(items):
            p = tf.add_paragraph() if i > 0 else tf.paragraphs[0]
            if isinstance(item, tuple):
                p.text = item[0] + ": "
                p.font.bold = True
                p.font.name = "Courier New" if is_code else "Arial"
                p.font.size = Pt(10 if is_code else 11.5)
                p.font.color.rgb = top_color

                run = p.add_run()
                run.text = item[1]
                run.font.bold = False
                run.font.name = "Courier New" if is_code else "Arial"
                run.font.size = Pt(10 if is_code else 11.5)
                run.font.color.rgb = TEXT_BODY
            else:
                p.text = item
                p.font.name = "Courier New" if is_code else "Arial"
                p.font.size = Pt(10 if is_code else 11.5)
                p.font.color.rgb = TEXT_BODY
            p.space_after = Pt(6)

    # ----------------------------------------------------
    # SLIDE 1: TITLE SLIDE (Executive Slate Dark)
    # ----------------------------------------------------
    s1 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s1, BG_DARK)

    # Overline Tag
    tag = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(1.8), Inches(3.2), Inches(0.35))
    tag.fill.solid()
    tag.fill.fore_color.rgb = RGBColor(30, 41, 59)
    tag.line.color.rgb = PRIMARY_BLUE
    tag.line.width = Pt(1)
    p_tag = tag.text_frame.paragraphs[0]
    p_tag.text = "EXECUTIVE BRIEFING & DEEP DIVE"
    p_tag.alignment = PP_ALIGN.CENTER
    p_tag.font.name = "Arial"
    p_tag.font.size = Pt(9.5)
    p_tag.font.bold = True
    p_tag.font.color.rgb = RGBColor(147, 197, 253)

    # Main Title
    tb = s1.shapes.add_textbox(Inches(0.8), Inches(2.3), Inches(11.7), Inches(2.0))
    tf = tb.text_frame
    tf.word_wrap = True
    p1 = tf.paragraphs[0]
    p1.text = "Demystifying LLM Reasoning"
    p1.font.name = "Arial"
    p1.font.size = Pt(36)
    p1.font.bold = True
    p1.font.color.rgb = TEXT_LIGHT

    p2 = tf.add_paragraph()
    p2.text = "How Modern Large Language Models Generate Code, Simulate Logic, and Process Thought"
    p2.font.name = "Arial"
    p2.font.size = Pt(16)
    p2.font.color.rgb = RGBColor(148, 163, 184)
    p2.space_before = Pt(8)

    # 3 Summary Pillar Cards
    pillars = [
        ("Architecture & Input", "Tokenization, context windows, and statistical pattern matching over sub-word embeddings.", Inches(0.8), PRIMARY_BLUE),
        ("Simulated Reasoning", "How multi-stage training (Pretraining, RLHF, Prompt Scaffolding) produces apparent logical deduction.", Inches(4.8), ACCENT_TEAL),
        ("Production & Code Quality", "Transitioning from naive memorization to robust edge-case validation and testing limitations.", Inches(8.8), ACCENT_INDIGO)
    ]
    for title, desc, x_pos, col in pillars:
        box = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos, Inches(4.7), Inches(3.73), Inches(1.9))
        box.fill.solid()
        box.fill.fore_color.rgb = RGBColor(30, 41, 59)
        box.line.color.rgb = RGBColor(51, 65, 85)
        box.line.width = Pt(1)

        st = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos, Inches(4.7), Inches(3.73), Inches(0.06))
        st.fill.solid()
        st.fill.fore_color.rgb = col
        st.line.fill.background()

        tf_b = box.text_frame
        tf_b.word_wrap = True
        p_h = tf_b.paragraphs[0]
        p_h.text = title
        p_h.font.name = "Arial"
        p_h.font.bold = True
        p_h.font.size = Pt(13)
        p_h.font.color.rgb = TEXT_LIGHT
        p_h.space_before = Pt(8)

        p_d = tf_b.add_paragraph()
        p_d.text = desc
        p_d.font.name = "Arial"
        p_d.font.size = Pt(11)
        p_d.font.color.rgb = RGBColor(148, 163, 184)
        p_d.space_before = Pt(6)

    # ----------------------------------------------------
    # SLIDE 2: THE REASONING PARADOX (Tokens vs Semantics)
    # ----------------------------------------------------
    s2 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s2, BG_LIGHT)
    add_header(s2, "Foundational Architecture: What an LLM Actually 'Sees'")

    add_card(s2, 0.8, 1.6, 3.7, 5.3, "1. Tokens, Not Concepts", [
        ("Sub-Word Chunks", "Text is tokenized into numeric vectors (~4 chars/token), not conceptual ideas or mental images."),
        ("Vector Space", "Tokens exist in high-dimensional embeddings where geometric distance reflects co-occurrence."),
        ("No Visual / Physical Grounding", "The model operates strictly on statistical distributions within textual data.")
    ], PRIMARY_BLUE)

    add_card(s2, 4.8, 1.6, 3.7, 5.3, "2. Finite Context Window", [
        ("Bounded Attention", "An LLM only evaluates tokens present in its current active context window."),
        ("Stateless Nature", "Each API call or generation step is stateless; memory is re-fed context sequentially."),
        ("No Hidden Working Memory", "All intermediate thoughts must be explicitly written out into the context tokens.")
    ], ACCENT_TEAL)

    add_card(s2, 8.8, 1.6, 3.7, 5.3, "3. Statistical Co-occurrence", [
        ("Next-Token Prediction", "At its core, the objective function is argmax P(token_t | tokens_1...t-1)."),
        ("Emerged Heuristics", "Sufficient scale and layers create internal representations that simulate formal reasoning rules."),
        ("Simulated vs Causal", "Logical conclusions are statistical continuations, not formal symbolic proofs.")
    ], ACCENT_AMBER)

    # ----------------------------------------------------
    # SLIDE 3: THREE-TIER EVOLUTION (Pretraining, RLHF, Prompt)
    # ----------------------------------------------------
    s3 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s3, BG_LIGHT)
    add_header(s3, "The 3 Stages of LLM Capability: Hardwired vs Transient State")

    add_card(s3, 0.8, 1.6, 3.7, 5.3, "Phase 1: Pre-training (Hardwired)", [
        ("Core Capability Layer", "Trained on trillion-token corpora spanning books, code, scientific papers, and web data."),
        ("Logic & Grammar", "Mathematical identities, algorithmic patterns, and transitivity rules are encoded in model weights."),
        ("Irreversible Base", "Weights are frozen post-training; this forms the model's fundamental ceiling of capability.")
    ], PRIMARY_BLUE)

    add_card(s3, 4.8, 1.6, 3.7, 5.3, "Phase 2: Fine-Tuning & RLHF", [
        ("Alignment & Behavior", "Reinforcement Learning from Human Feedback aligns raw completions to human intent."),
        ("Step-by-Step Framing", "Incentivizes chain-of-thought, structured breakdowns, safety filtering, and helpful tone."),
        ("Stylistic Polish", "Does not introduce net-new fundamental reasoning, but unlocks and structures latent capability.")
    ], ACCENT_INDIGO)

    add_card(s3, 8.8, 1.6, 3.7, 5.3, "Phase 3: Prompt & Inference", [
        ("Contextual Scaffolding", "The active prompt directs pretrained weights toward a specific domain or problem instance."),
        ("Zero Weight Updates", "No weights change during inference; behavior is conditioned purely via context activations."),
        ("Transient State", "Session memory clears once the context window ends.")
    ], ACCENT_TEAL)

    # ----------------------------------------------------
    # SLIDE 4: LOGICAL DEDUCTION IN ACTION (Alice / Bob)
    # ----------------------------------------------------
    s4 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s4, BG_LIGHT)
    add_header(s4, "Case Study: Logical Transitivity & Entity Recognition")

    add_card(s4, 0.8, 1.6, 5.7, 5.3, "Transitivity Evaluation", [
        ("Input Prompt", "\"If Alice is taller than Bob, and Bob is taller than Charlie, who is the tallest?\""),
        ("Model Derivation", "1. Relation 1: Height(Alice) > Height(Bob)\n2. Relation 2: Height(Bob) > Height(Charlie)\n3. Deduction: Height(Alice) > Height(Charlie)\n4. Conclusion: Alice is the tallest."),
        ("Underlying Mechanism", "The model leverages pretrained algebraic/transitive relational patterns (A > B ∧ B > C => A > C)."),
        ("RLHF Influence", "RLHF instructs the model to format the derivation incrementally rather than returning a raw token.")
    ], PRIMARY_BLUE)

    add_card(s4, 6.8, 1.6, 5.7, 5.3, "How the Model Understands Entities", [
        ("Statistical Association", "Tokens 'Alice' and 'Bob' have high distributional affinity with proper human nouns."),
        ("Syntactic Context", "Phrases like 'taller than' typically bind to physical entities, filtering out inanimate object interpretations."),
        ("Embedding Proximity", "Vector representations of names cluster closely to biographical and character contexts in latent space."),
        ("Important Distinction", "The LLM does not conceptualize biological human beings; it models syntactic and semantic token interactions.")
    ], ACCENT_TEAL)

    # ----------------------------------------------------
    # SLIDE 5: COMPUTATIONAL REASONING (Math Word Problems)
    # ----------------------------------------------------
    s5 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s5, BG_LIGHT)
    add_header(s5, "Multi-Step Mathematical Reasoning: Successes and Pitfalls")

    add_card(s5, 0.8, 1.6, 5.7, 5.3, "Kinematics Problem Breakdown", [
        ("Scenario", "Train A leaves at 2 PM (60 km/h); Train B leaves at 3 PM (90 km/h) toward A. Distance = 300 km."),
        ("Step 1: Offset Adjustment", "From 2 PM to 3 PM, Train A covers 60 km. Remaining distance = 240 km."),
        ("Step 2: Relative Velocity", "Closing velocity = 60 + 90 = 150 km/h."),
        ("Step 3: Time Elapsed", "t = 240 / 150 = 1.6 hours = 1 hour 36 minutes."),
        ("Final Result", "Meeting time = 3:00 PM + 1h 36m = 4:36 PM."),
        ("Nature of Task", "Cannot be solved by static memorization; requires chaining formulas dynamically.")
    ], ACCENT_TEAL)

    add_card(s5, 6.8, 1.6, 5.7, 5.3, "Common Failure Modes in LLM Math", [
        ("Unit & Decimal Conversions", "Frequent failure to convert decimal hours correctly (e.g., assuming 0.1 hours = 10 minutes instead of 6 minutes)."),
        ("Boundary Condition Drops", "Skipping staggered start times or dropping edge-case offsets during long derivations."),
        ("Floating Point Drift", "Accumulation of arithmetic errors in intermediate token generation steps."),
        ("Hallucinated Formulas", "Generating plausibly phrased equations that are mathematically invalid.")
    ], ACCENT_ROSE)

    # ----------------------------------------------------
    # SLIDE 6: CODE GENERATION (Naive vs Defensive)
    # ----------------------------------------------------
    s6 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s6, BG_LIGHT)
    add_header(s6, "Code Generation: Naive Completion vs Defensive Engineering")

    add_card(s6, 0.8, 1.6, 5.7, 5.3, "Naive Code Generation (Pattern Recall)", [
        ("Context", "Vague prompt: 'Write a Python function to divide two numbers.'"),
        ("Generated Implementation", "def divide(a, b):\n    return a / b"),
        ("Vulnerabilities", "• Zero type validation (crashes on string inputs)\n• Zero division handling (throws unhandled ZeroDivisionError)\n• Lacks type annotations or docstrings"),
        ("Root Cause", "Matches the most frequent, minimal code snippet found in basic tutorials and standard repos.")
    ], ACCENT_AMBER, is_code=False)

    add_card(s6, 6.8, 1.6, 5.7, 5.3, "Defensive Code Generation (Reasoned Synthesis)", [
        ("Context", "Explicit prompt: 'Write divide function handling types and edge cases.'"),
        ("Generated Implementation", "def divide(a: float, b: float) -> float:\n    if not isinstance(a, (int, float)) or \\\n       not isinstance(b, (int, float)):\n        raise TypeError('Inputs must be numeric')\n    if b == 0:\n        raise ValueError('Denominator cannot be zero')\n    return a / b"),
        ("Engineering Value", "• Robust input validation\n• Explicit error taxonomy\n• Full type hinting and signature safety")
    ], PRIMARY_BLUE, is_code=False)

    # ----------------------------------------------------
    # SLIDE 7: OOP & ARCHITECTURE (Calculator Class)
    # ----------------------------------------------------
    s7 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s7, BG_LIGHT)
    add_header(s7, "Architectural Synthesis: DRY Principle & Modular Encapsulation")

    add_card(s7, 0.8, 1.6, 5.7, 5.3, "Naive OOP Implementation", [
        ("Pattern", "Repetitive in-method logic without shared encapsulation:"),
        ("Code Snippet", "class Calculator:\n    def add(self, a, b): return a + b\n    def subtract(self, a, b): return a - b\n    def multiply(self, a, b): return a * b\n    def divide(self, a, b):\n        if b == 0: raise ValueError('Zero division')\n        return a / b"),
        ("Drawbacks", "Duplication of assumptions, missing centralized input validation across methods.")
    ], ACCENT_SLATE)

    add_card(s7, 6.8, 1.6, 5.7, 5.3, "Enterprise Refactored Implementation", [
        ("Pattern", "Encapsulated static validation adhering to DRY standards:"),
        ("Code Snippet", "class Calculator:\n    @staticmethod\n    def _validate_inputs(a, b):\n        if not isinstance(a, (int, float)) or \\\n           not isinstance(b, (int, float)):\n            raise TypeError('Inputs must be numeric')\n\n    def divide(self, a: float, b: float) -> float:\n        self._validate_inputs(a, b)\n        if b == 0: raise ValueError('Denominator cannot be zero')\n        return a / b"),
        ("Key Insight", "Prompt specificity directs the model from casual snippets to enterprise design patterns.")
    ], ACCENT_INDIGO)

    # ----------------------------------------------------
    # SLIDE 8: SIMULATED TESTING & POST-HOC RATIONALE
    # ----------------------------------------------------
    s8 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s8, BG_LIGHT)
    add_header(s8, "The Illusion of Execution: Simulated Testing & Post-Hoc Narratives")

    add_card(s8, 0.8, 1.6, 3.7, 5.3, "1. Simulated Unit Tests", [
        ("No Execution Runtime", "When an LLM writes test assertions (e.g., assertEqual), it never runs an interpreter."),
        ("Syntactic Mimicry", "It outputs tokens that resemble valid unit tests based on learned testing patterns."),
        ("Validation Mandate", "All AI-generated test suites require external execution in isolated CI/CD sandboxes.")
    ], PRIMARY_BLUE)

    add_card(s8, 4.8, 1.6, 3.7, 5.3, "2. Post-Hoc Rationalization", [
        ("Narrative Construction", "Explanations generated by LLMs are constructed after the primary token predictions occur."),
        ("Not the Causal Path", "The model's explanation does not represent the internal matrix weight activations."),
        ("Plausibility Bias", "The LLM will generate confident, coherent justifications even for incorrect calculations.")
    ], ACCENT_AMBER)

    add_card(s8, 8.8, 1.6, 3.7, 5.3, "3. Human-in-the-Loop Governance", [
        ("Supervised Output", "LLMs serve as high-throughput draft generators, not autonomous decision authorities."),
        ("Compiler as Arbiter", "Always verify computational, cryptographic, and algorithmic output with deterministic tooling."),
        ("Verification Workflows", "Pair LLMs with static analysis, linters, and runtime testing frameworks.")
    ], ACCENT_TEAL)

    # ----------------------------------------------------
    # SLIDE 9: EXECUTIVE SUMMARY & STRATEGIC TAKEAWAYS
    # ----------------------------------------------------
    s9 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s9, BG_DARK)
    add_header(s9, "Strategic Takeaways & Best Practices", "EXECUTIVE SUMMARY", is_dark=True)

    takeaways = [
        ("1. Reasoning is Simulated", "LLMs do not perform true symbolic deduction; they reproduce complex, learned statistical pathways that mimic logical thought. Calibrate reliance accordingly.", Inches(0.8), PRIMARY_BLUE),
        ("2. Prompt Quality Shapes Rigor", "The gap between buggy code and production-grade software is prompt constraint. Specify types, exceptions, edge conditions, and design architectures explicitly.", Inches(4.8), ACCENT_TEAL),
        ("3. Enforce Deterministic Validation", "Never trust AI-generated arithmetic or test passes without execution. Always bridge LLM output into compilers, linters, unit test runners, and human code reviews.", Inches(8.8), ACCENT_INDIGO)
    ]

    for title, text, x_pos, col in takeaways:
        card = s9.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos, Inches(1.8), Inches(3.73), Inches(5.0))
        card.fill.solid()
        card.fill.fore_color.rgb = RGBColor(30, 41, 59)
        card.line.color.rgb = RGBColor(51, 65, 85)
        card.line.width = Pt(1)

        # Top stripe
        st = s9.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x_pos, Inches(1.8), Inches(3.73), Inches(0.08))
        st.fill.solid()
        st.fill.fore_color.rgb = col
        st.line.fill.background()

        # Header tag
        tb_t = s9.shapes.add_textbox(x_pos + Inches(0.2), Inches(2.0), Inches(3.33), Inches(0.8))
        tf_t = tb_t.text_frame
        tf_t.word_wrap = True
        p_t = tf_t.paragraphs[0]
        p_t.text = title
        p_t.font.name = "Arial"
        p_t.font.size = Pt(14)
        p_t.font.bold = True
        p_t.font.color.rgb = TEXT_LIGHT

        # Body
        tb = s9.shapes.add_textbox(x_pos + Inches(0.2), Inches(2.85), Inches(3.33), Inches(3.7))
        tf = tb.text_frame
        tf.word_wrap = True
        p = tf.paragraphs[0]
        p.text = text
        p.font.name = "Arial"
        p.font.size = Pt(12)
        p.font.color.rgb = RGBColor(203, 213, 225)
        p.space_before = Pt(8)

    output_path = "c:/WebSearch_GO/LLM_Reasoning_Executive_Guide.pptx"
    prs.save(output_path)
    print(f"Professional presentation saved successfully to {output_path}")

if __name__ == "__main__":
    create_pro_deck()
